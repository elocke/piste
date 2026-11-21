"""piste-guard: PreToolUse safety guard for Claude Code and Codex.

Declared in agents/hooks.toml, rendered for both harnesses by piste.agents.
A safety guard (dotenv, git add, ...) that replaces the plugin listed under
[replaces] in hooks.toml, without that plugin's false positives:

  .env files   deny reading a dotenv file (Read/Grep tools, or cat/grep/... in
               Bash). Templates (.env.example, .env.sample, ...) are allowed.
  git add      deny whole-tree adds: -A/--all, `.`, `..`, `:/`, and wildcard
               pathspecs. `git add <files>`, `git add dir/`, `-u` are fine.
  git commit   secret scan (gitleaks) of what the commit would record; deny
               on a finding. No approval prompt on commit.
  git discard  deny reset --hard / checkout -f / checkout -- <p> / restore <p>
               / clean -f only when they would destroy uncommitted work;
               deny push --force without --force-with-lease, and push
               --no-verify (the push hook is the leak gate).
  rm           allowed when every target is recoverable (clean, fully
               tracked by git), temporary (/tmp, $TMPDIR), or absent;
               otherwise deny and suggest `trash-put`.
  main checkout  deny the edit tools (Edit, Write, MultiEdit, NotebookEdit)
               inside a main checkout listed in ~/.config/piste/protected-checkouts
               ($PISTE_GUARD_PROTECTED overrides the path): one absolute path
               per line, `#` comments, and an optional `hint: <text>` line
               added to the reason, with {root} replaced by the checkout. A
               linked worktree of that repo is not its main checkout, so edits
               there are fine. No list file, no effect.

Input: the hook JSON on stdin (tool_name, tool_input, cwd). Output: nothing
(no opinion; the harness permission flow decides) or a PreToolUse "deny".
Both harnesses read the same output shape.

Fails open: an internal error prints to stderr and allows the call, so a bug
here never blocks every tool call. PISTE_GUARD_DISABLE=1 in the harness
environment turns the guard off.

Stdlib only; gitleaks is found through PISTE_GUARD_GITLEAKS or PATH.
"""

import fnmatch
import glob
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile

TEMPLATE_SUFFIXES = (".example", ".sample", ".template", ".dist", ".defaults")

# Commands that print (part of) a file they are given as an argument.
READERS = {
    "cat", "tac", "nl", "less", "more", "most", "head", "tail", "bat", "batcat",
    "strings", "xxd", "od", "hexdump", "hexyl", "base64", "grep", "egrep",
    "fgrep", "rg", "ag", "ack", "sed", "awk", "gawk", "cut", "sort", "uniq",
    "diff", "jq", "yq", "column", "paste", "fold", "fmt", "pr", "view", "read",
}
# Prefix commands whose arguments are another command.
WRAPPERS = {"sudo", "doas", "command", "builtin", "exec", "nohup", "time",
            "nice", "ionice", "timeout", "stdbuf", "env", "xargs", "rtk"}
SHELLS = {"sh", "bash", "zsh", "dash", "ksh"}
TEMP_ROOTS = ("/tmp", "/var/tmp", "/dev/shm")
GIT_TIMEOUT = 10
SCAN_TIMEOUT = 30


# --- tokenizing ----------------------------------------------------------------

def strip_heredocs(text):
    """Drop heredoc bodies: their lines are data, not commands."""
    lines = text.split("\n")
    out = []
    i = 0
    while i < len(lines):
        line = lines[i]
        out.append(line)
        i += 1
        for dash, _, delim in re.findall(r"(?<!<)<<(?!<)(-?)\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\2", line):
            while i < len(lines) and (lines[i].strip() if dash else lines[i]) != delim:
                i += 1
            i += 1  # the delimiter line
    return "\n".join(out)


def tokens(text):
    text = strip_heredocs(text).replace("`", "\n")
    lex = shlex.shlex(text, posix=True, punctuation_chars=";&|()<>\n")
    lex.whitespace = " \t\r"
    lex.whitespace_split = True
    lex.commenters = ""
    try:
        return list(lex)
    except ValueError:  # unbalanced quotes: fall back to a rough split
        return text.replace(";", " ; ").replace("&&", " && ").replace("|", " | ").split()


def is_operator(tok):
    return tok != "" and all(c in ";&|()<>\n" for c in tok)


def is_redirect(tok):
    return is_operator(tok) and ("<" in tok or ">" in tok) and not any(c in tok for c in ";|()\n")


def simple_commands(text):
    """Yield (argv, stdin_files) for each simple command in a shell string."""
    toks = tokens(text)
    argv, inputs = [], []
    i = 0
    while i < len(toks):
        tok = toks[i]
        if is_redirect(tok):
            target = toks[i + 1] if i + 1 < len(toks) else ""
            if "<" in tok and "<<" not in tok:
                inputs.append(target)
            if argv and argv[-1].isdigit():
                argv.pop()
            i += 2
            continue
        if is_operator(tok):
            if argv or inputs:
                yield argv, inputs
            argv, inputs = [], []
        else:
            argv.append(tok)
        i += 1
    if argv or inputs:
        yield argv, inputs


def unwrap(argv):
    """Strip assignments and prefix commands; return (argv, via_xargs)."""
    via_xargs = False
    while argv:
        head = os.path.basename(argv[0])
        if re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", argv[0]):
            argv = argv[1:]
        elif head == "rtk" and len(argv) > 1 and argv[1] in ("proxy", "run", "err", "test", "summary"):
            argv = argv[2:]
        elif head in WRAPPERS:
            via_xargs = via_xargs or head == "xargs"
            argv = argv[1:]
            # Drop the wrapper's own options (and a timeout/nice value).
            while argv and (argv[0].startswith("-") or re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", argv[0])
                            or (head in ("timeout", "nice") and re.match(r"^[0-9.]+[smhd]?$", argv[0]))):
                flag = argv[0]
                argv = argv[1:]
                if head in ("sudo", "xargs", "nice", "ionice", "stdbuf") and flag in ("-u", "-g", "-n", "-I", "-L", "-P", "-d", "-c", "-o", "-e", "-i"):
                    argv = argv[1:]
        else:
            break
    return argv, via_xargs


# --- helpers -------------------------------------------------------------------

def is_env_file(path):
    base = os.path.basename(path.rstrip("/")).lower()
    if not base:
        return False
    if base == ".env" or base.startswith(".env.") or (base.endswith(".env") and len(base) > 4):
        stem = base[:-4] if base.endswith(".env") and base != ".env" else base
        return not any(base.endswith(s) or stem.endswith(s) for s in TEMPLATE_SUFFIXES)
    return False


def git(cwd, *args):
    try:
        return subprocess.run(["git", "-C", cwd, *args], capture_output=True, text=True, timeout=GIT_TIMEOUT)
    except (OSError, subprocess.TimeoutExpired):
        return None


def git_ok(cwd, *args):
    r = git(cwd, *args)
    return r is not None and r.returncode == 0


def git_out(cwd, *args):
    r = git(cwd, *args)
    return r.stdout if r is not None and r.returncode == 0 else ""


def temp_roots():
    roots = list(TEMP_ROOTS)
    if os.environ.get("TMPDIR"):
        roots.append(os.environ["TMPDIR"].rstrip("/"))
    return [os.path.realpath(r) for r in roots]


def under(path, root):
    return path == root or path.startswith(root.rstrip("/") + "/")


def resolve(cwd, word):
    """Expand ~, $VARS and globs the way the shell would; None if unresolvable."""
    word = os.path.expanduser(os.path.expandvars(word))
    if "$" in word:
        return None
    path = word if os.path.isabs(word) else os.path.join(cwd, word)
    if any(c in word for c in "*?["):
        hits = glob.glob(path)
        return [os.path.abspath(h) for h in hits] or [os.path.abspath(path)]
    return [os.path.abspath(path)]


# --- .env ------------------------------------------------------------------------

ENV_REASON = (
    "piste-guard: reading a dotenv file ({path}) is blocked: it holds secrets. "
    "Use the template (.env.example) for variable names, or ask the user for "
    "the value you need."
)


def check_env_words(cmd, args, inputs):
    if cmd in READERS:
        for a in args:
            value = a.split("=", 1)[1] if a.startswith("--") and "=" in a else a
            if not value.startswith("-") and is_env_file(value):
                return ENV_REASON.format(path=value)
    for f in inputs:
        if is_env_file(f):
            return ENV_REASON.format(path=f)
    return None


# --- rm --------------------------------------------------------------------------

def rm_targets(args):
    targets, opts_done = [], False
    for a in args:
        if not opts_done and a == "--":
            opts_done = True
        elif not opts_done and a.startswith("-") and a != "-":
            continue
        else:
            targets.append(a)
    return targets


def recoverable(path):
    """Why `path` may be removed safely, or None."""
    if not os.path.lexists(path):
        return "absent"
    real_parent = os.path.realpath(os.path.dirname(path))
    real = os.path.join(real_parent, os.path.basename(path))
    if any(under(real, r) and real != r for r in temp_roots()):
        return "temporary"
    probe = real if os.path.isdir(real) and not os.path.islink(real) else real_parent
    top = git_out(probe, "rev-parse", "--show-toplevel").strip()
    if not top:
        return None
    rel = os.path.relpath(real, top)
    if rel == "." or rel.startswith("..") or rel.split(os.sep)[0] == ".git":
        return None  # the repo itself, or its .git: not recoverable from git
    tracked = git_out(top, "ls-files", "--", rel)
    others = git_out(top, "ls-files", "--others", "--", rel)  # untracked + ignored
    clean = git_ok(top, "diff", "--quiet", "HEAD", "--", rel)
    nested_git = os.path.isdir(real) and not os.path.islink(real) and any(
        ".git" in dirs or ".git" in files for _, dirs, files in os.walk(real))
    if tracked and not others and clean and not nested_git:
        return "tracked"
    return None


def check_rm(args, cwd, via_xargs):
    if via_xargs:
        return ("piste-guard: `xargs rm` targets can't be checked. Use `trash-put` "
                "(recoverable: trash-list / trash-restore) instead.")
    bad = []
    for word in rm_targets(args):
        paths = resolve(cwd, word)
        if paths is None:
            bad.append(word)
            continue
        bad.extend(p for p in paths if recoverable(p) is None)
    if not bad:
        return None
    shown = ", ".join(bad[:5]) + (f" (+{len(bad) - 5} more)" if len(bad) > 5 else "")
    return (f"piste-guard: rm would permanently delete {shown}: untracked, ignored or modified "
            "in git (or outside a repo), and not under /tmp. Use `trash-put <paths>` instead "
            "(recoverable with trash-list / trash-restore). rm is allowed for /tmp, $TMPDIR "
            "and files that git can restore (tracked, unmodified).")


# --- git -------------------------------------------------------------------------

GIT_VALUE_OPTS = {"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--config-env"}


def split_git(args, cwd):
    """(subcommand, subargs, cwd) after git's global options."""
    i = 0
    while i < len(args):
        a = args[i]
        if a == "-C" and i + 1 < len(args):
            nxt = os.path.expanduser(args[i + 1])
            cwd = nxt if os.path.isabs(nxt) else os.path.join(cwd, nxt)
            i += 2
        elif a in GIT_VALUE_OPTS:
            i += 2
        elif a.startswith("-"):
            i += 1
        else:
            return a, args[i + 1:], cwd
    return None, [], cwd


def short_flags(args, value_flags=""):
    """Letters from short-option clusters, stopping at a value-taking letter."""
    letters = set()
    for a in args:
        if a == "--":
            break
        if re.match(r"^-[A-Za-z]", a) and not a.startswith("--"):
            for ch in a[1:]:
                letters.add(ch)
                if ch in value_flags:
                    break
    return letters


def pathspecs(args, value_opts=()):
    """Non-option arguments (everything after `--` counts)."""
    out, skip, done = [], False, False
    for a in args:
        if skip:
            skip = False
        elif done:
            out.append(a)
        elif a == "--":
            done = True
        elif a in value_opts:
            skip = True
        elif not a.startswith("-") or a == "-":
            out.append(a)
    return out


def check_git_add(args):
    longs = {a.split("=")[0] for a in args if a.startswith("--")}
    if "--dry-run" in longs or "n" in short_flags(args):
        return None
    if longs & {"--all", "--no-ignore-removal"} or "A" in short_flags(args):
        return ("piste-guard: `git add -A/--all` stages every change, including stray and "
                "secret files. Stage explicit paths (`git add <files>` or `git add <dir>/`), "
                "or `git add -u` for tracked changes only.")
    for p in pathspecs(args, {"--chmod", "--pathspec-from-file"}):
        if p in (".", "./", "..", "../", ":/", ":", "*") or re.fullmatch(r"(\.\./)+\.?\.?/?", p):
            return (f"piste-guard: `git add {p}` stages the whole tree. Stage explicit paths "
                    "(`git add <files>` or `git add <dir>/`), or `git add -u`.")
        if any(c in p for c in "*?[") or p.startswith(":("):
            return (f"piste-guard: wildcard pathspec `{p}` in git add. Stage explicit paths "
                    "(`git add <files>` / `git add <dir>/`).")
    return None


def gitleaks_bin():
    return os.environ.get("PISTE_GUARD_GITLEAKS") or shutil.which("gitleaks")


def scan(top, mode, path=None):
    """gitleaks findings (list of dicts). mode: "staged" / "unstaged" diff of
    the repo at `top`, or "file" for the current content of `path`."""
    exe = gitleaks_bin()
    fd, report = tempfile.mkstemp(prefix="piste-guard-", suffix=".json")
    os.close(fd)
    try:
        common = ["--no-banner", "--redact", "--log-level", "error", "--exit-code", "3",
                  "--report-format", "json", "--report-path", report]
        if mode == "file":
            cmd = [exe, "dir", *common, path]
        else:
            cmd = [exe, "git", "--pre-commit", *common] + (["--staged"] if mode == "staged" else []) + [top]
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=SCAN_TIMEOUT, cwd=top)
        if r.returncode not in (0, 3):
            print(f"piste-guard: gitleaks failed ({r.returncode}): {r.stderr.strip()[:300]}", file=sys.stderr)
            return []
        with open(report) as fh:
            text = fh.read().strip()
        return json.loads(text) if text else []
    finally:
        os.unlink(report)


def check_git_commit(args, cwd, pending=None):
    """Scan what the commit would record. `pending` is what earlier commands
    in the same line will stage first ({"paths": [...], "update": bool}): the
    hook runs before any of it, so the index doesn't show it yet."""
    pending = pending or {"paths": [], "update": False}
    exe = gitleaks_bin()
    if not exe:
        print("piste-guard: gitleaks not found; commit not scanned for secrets", file=sys.stderr)
        return None
    top = git_out(cwd, "rev-parse", "--show-toplevel").strip()
    if not top:
        return None
    longs = {a.split("=")[0] for a in args if a.startswith("--")}
    value_opts = {"-m", "-F", "-c", "-C", "--message", "--file", "--author", "--date",
                  "--fixup", "--squash", "--reuse-message", "--reedit-message", "--cleanup",
                  "--trailer", "-t", "--template", "--pathspec-from-file"}
    specs = pathspecs(args, value_opts)
    worktree = ("a" in short_flags(args, "mFcCt")) or bool(longs & {"--all"}) or pending["update"]
    files = [os.path.join(cwd, p) for p in specs] + pending["paths"]
    files = [f for f in dict.fromkeys(os.path.normpath(f) for f in files) if os.path.exists(f)][:50]
    try:
        findings = scan(top, "staged") + (scan(top, "unstaged") if worktree else [])
        for f in files:
            findings += scan(top, "file", f)
    except subprocess.TimeoutExpired:
        print("piste-guard: gitleaks timed out; commit not scanned", file=sys.stderr)
        return None
    if not findings:
        return None
    rows = sorted({f"{f.get('File')}:{f.get('StartLine')} ({f.get('RuleID')})" for f in findings})
    return ("piste-guard: gitleaks found possible secrets in this commit:\n  " + "\n  ".join(rows[:10])
            + ("\n  ..." if len(rows) > 10 else "")
            + "\nRemove them (unstage, move the value to an env var / sops). If one is a "
              "false positive, add a `gitleaks:allow` comment on that line or a "
              ".gitleaksignore entry, then commit again.")


def dirty(cwd, paths=()):
    """Tracked files with uncommitted changes (staged or not) under paths."""
    out = git_out(cwd, "status", "--porcelain", "--untracked-files=no", "--", *(paths or ["."]))
    return [line[3:] for line in out.splitlines() if line.strip()]


def lose(what, files):
    shown = ", ".join(files[:5]) + (f" (+{len(files) - 5} more)" if len(files) > 5 else "")
    return (f"piste-guard: {what} would discard uncommitted changes in {shown}. "
            "Commit or `git stash` them first (or show the user the diff and ask).")


def check_git_discard(sub, args, cwd):
    flags = short_flags(args)
    longs = {a.split("=")[0] for a in args if a.startswith("--")}
    if sub == "reset" and "--hard" in longs:
        files = dirty(cwd)
        return lose("git reset --hard", files) if files else None
    if sub == "checkout":
        if "-b" in flags or "B" in flags or "--help" in longs:
            return None
        specs = pathspecs(args, {"-b", "-B", "--orphan", "--conflict", "--pathspec-from-file"})
        if "--" in args:
            paths = args[args.index("--") + 1:]
        elif "." in specs:
            paths = ["."]
        elif "f" in flags or "--force" in longs:
            paths = ["."]
        else:
            return None  # branch switch: git itself refuses to clobber changes
        files = dirty(cwd, paths)
        return lose("git checkout", files) if files else None
    if sub == "restore":
        staged_only = ("S" in flags or "--staged" in longs) and not ("W" in flags or "--worktree" in longs)
        if staged_only:
            return None
        files = dirty(cwd, pathspecs(args, {"-s", "--source", "--pathspec-from-file"}) or ["."])
        return lose("git restore", files) if files else None
    if sub == "clean":
        if "n" in flags or "--dry-run" in longs or not ("f" in flags or "--force" in longs):
            return None
        dry = ["clean", "-n"] + [f"-{c}" for c in flags & set("dxX")] + ["--"] + pathspecs(args, {"-e", "--exclude"})
        out = git_out(cwd, *dry).splitlines()
        if not out:
            return None
        files = [line.replace("Would remove ", "") for line in out]
        return ("piste-guard: git clean would permanently delete untracked files: "
                + ", ".join(files[:5]) + (f" (+{len(files) - 5} more)" if len(files) > 5 else "")
                + ". Move what you want gone with `trash-put`, or ask the user.")
    if sub == "push":
        # git takes any unambiguous prefix of a long option (--no-veri).
        if any(len(a) >= len("--no-veri") and "--no-verify".startswith(a) for a in longs):
            return ("piste-guard: `git push --no-verify` skips the push hook, and the push hook "
                    "is the leak gate. Push without it; if the hook blocks, take out what it found.")
        lease = any(a.startswith(("--force-with-lease", "--force-if-includes")) for a in args)
        forced = "f" in flags or "--force" in longs or any(
            a.startswith("+") for a in pathspecs(args, {"-o", "--push-option", "--repo", "--receive-pack", "--exec"}))
        if forced and not lease:
            return ("piste-guard: `git push --force` can overwrite others' commits. Use "
                    "`git push --force-with-lease` (and only on your own branch).")
    return None


# --- dispatch --------------------------------------------------------------------

def check_command(text, cwd, depth=0, pending=None):
    reasons = []
    pending = pending if pending is not None else {"paths": [], "update": False}
    for argv, inputs in simple_commands(text):
        if argv and argv[0].startswith("#"):
            continue
        argv = [a for a in argv if a]
        comment = next((i for i, a in enumerate(argv) if a.startswith("#") and i > 0), None)
        if comment is not None:
            argv = argv[:comment]
        argv, via_xargs = unwrap(argv)
        env_reason = check_env_words(os.path.basename(argv[0]) if argv else "", argv[1:], inputs)
        if env_reason:
            reasons.append(env_reason)
        if not argv:
            continue
        cmd, args = os.path.basename(argv[0]), argv[1:]
        if cmd == "cd" and args:
            target = os.path.expanduser(os.path.expandvars(args[-1]))
            cwd = target if os.path.isabs(target) else os.path.normpath(os.path.join(cwd, target))
        elif cmd in SHELLS and "-c" in args and depth < 3:
            i = args.index("-c")
            if i + 1 < len(args):
                reasons.extend(check_command(args[i + 1], cwd, depth + 1, pending))
        elif cmd == "eval" and depth < 3:
            reasons.extend(check_command(" ".join(args), cwd, depth + 1, pending))
        elif cmd in ("rm", "unlink"):
            r = check_rm(args, cwd, via_xargs)
            if r:
                reasons.append(r)
        elif cmd == "git":
            sub, subargs, gcwd = split_git(args, cwd)
            r = None
            if sub == "add":
                r = check_git_add(subargs)
                pending["paths"] += [os.path.join(gcwd, p) for p in pathspecs(subargs, {"--chmod", "--pathspec-from-file"})]
                pending["update"] = pending["update"] or "u" in short_flags(subargs) or "--update" in subargs
            elif sub == "commit":
                r = check_git_commit(subargs, gcwd, pending)
            elif sub in ("reset", "checkout", "restore", "clean", "push"):
                r = check_git_discard(sub, subargs, gcwd)
            if r:
                reasons.append(r)
    return reasons


PROTECTED_FILE = "~/.config/piste/protected-checkouts"
EDIT_TOOLS = ("Edit", "Write", "MultiEdit", "NotebookEdit")
MAIN_REASON = (
    "{root} is a protected main checkout: other agents share it, so edits go in a "
    "separate worktree of this repo instead."
)
LINK_REASON = (
    "{path} is a link into {root}, a protected main checkout: every copy of this repo "
    "shares that file. Don't edit or replace the link; ask the user to change it in {root}."
)


def load_protected():
    """(roots, hint) from the protected-checkouts list; ([], "") without one."""
    path = os.path.expanduser(os.environ.get("PISTE_GUARD_PROTECTED", PROTECTED_FILE))
    try:
        with open(path, encoding="utf-8") as f:
            lines = f.read().splitlines()
    except OSError:
        return [], ""
    roots, hint = [], ""
    for line in (l.strip() for l in lines):
        if not line or line.startswith("#"):
            continue
        if line.startswith("hint:"):
            hint = line[len("hint:"):].strip()
            continue
        root = os.path.realpath(os.path.expanduser(line))
        if os.path.isdir(root):
            roots.append(root)
    return roots, hint


def toplevel(path):
    """realpath of the repo toplevel holding path's nearest existing folder, or ""."""
    folder = path if os.path.isdir(path) else os.path.dirname(path)
    while folder and not os.path.isdir(folder):
        folder = os.path.dirname(folder)
    top = git_out(folder, "rev-parse", "--show-toplevel").strip()
    return os.path.realpath(top) if top else ""


def check_main_checkout(path, cwd):
    """Deny an edit whose target's repo toplevel is a protected main checkout."""
    if not path:
        return []
    roots, hint = load_protected()
    if not roots:
        return []
    given = os.path.abspath(os.path.join(cwd, os.path.expanduser(path)))
    top = toplevel(os.path.realpath(given))
    if top not in roots:
        return []
    # A link from elsewhere (another worktree) into the root: a new worktree
    # would hold the same link, so don't send the agent off to make one.
    if toplevel(os.path.dirname(given)) != top:
        return [LINK_REASON.format(path=given, root=top)]
    reason = MAIN_REASON.format(root=top)
    return [reason + ("\n" + hint.replace("{root}", top) if hint else "")]


def check_tool(data):
    tool = data.get("tool_name") or ""
    ti = data.get("tool_input") or {}
    cwd = data.get("cwd") or os.getcwd()
    if tool == "Bash":
        return check_command(ti.get("command") or "", cwd)
    if tool in EDIT_TOOLS:
        return check_main_checkout(ti.get("file_path") or ti.get("notebook_path") or "", cwd)
    if tool == "Read" and is_env_file(ti.get("file_path") or ""):
        return [ENV_REASON.format(path=ti["file_path"])]
    if tool == "Grep":
        path, pattern = ti.get("path") or "", ti.get("glob") or ""
        if is_env_file(path) or (pattern and any(fnmatch.fnmatch(n, pattern) for n in (".env", ".env.local", "prod.env"))):
            return [ENV_REASON.format(path=path or pattern)]
    return []


def main():
    if os.environ.get("PISTE_GUARD_DISABLE") == "1":
        return 0
    try:
        data = json.load(sys.stdin)
        reasons = check_tool(data)
    except Exception as err:  # fail open: never block every call on a guard bug
        print(f"piste-guard: internal error, allowing: {err!r}", file=sys.stderr)
        return 0
    if reasons:
        print(json.dumps({
            "hookSpecificOutput": {
                "hookEventName": "PreToolUse",
                "permissionDecision": "deny",
                "permissionDecisionReason": "\n\n".join(dict.fromkeys(reasons)),
            }
        }))
    return 0


if __name__ == "__main__":
    sys.exit(main())
