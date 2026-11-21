"""Tests for agents/hooks/piste-guard.py (the PreToolUse guard).

Run: python3 -m unittest discover -s tests/agents
Stdlib only. The secret-scan cases need gitleaks and are skipped without it.
The same cases hold for Claude and Codex: both send tool_name "Bash" with
tool_input.command, and read the same deny output.
"""

import importlib.util
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

HERE = Path(__file__).resolve().parent
TOOL = Path(os.environ.get("PISTE_GUARD_PY", HERE.parent.parent / "agents/hooks/piste-guard.py"))
spec = importlib.util.spec_from_file_location("piste_guard", TOOL)
guard = importlib.util.module_from_spec(spec)
sys.modules["piste_guard"] = guard
spec.loader.exec_module(guard)

RM = "r" + "m"  # keep the literal out of shell history greps


def sh(cwd, *args):
    subprocess.run(args, cwd=cwd, check=True, capture_output=True)


class Repo(unittest.TestCase):
    """A temp git repo outside /tmp-style temp roots, so rm rules apply."""

    def setUp(self):
        base = Path.home() / ".cache" / "piste-guard-tests"
        base.mkdir(parents=True, exist_ok=True)
        self._tmp = tempfile.TemporaryDirectory(dir=base)
        self.repo = Path(self._tmp.name) / "repo"
        self.repo.mkdir()
        sh(self.repo, "git", "init", "-q", "-b", "main")
        sh(self.repo, "git", "config", "user.email", "t@example.com")
        sh(self.repo, "git", "config", "user.name", "t")
        sh(self.repo, "git", "config", "commit.gpgsign", "false")
        (self.repo / "tracked.txt").write_text("one\n")
        (self.repo / "dir").mkdir()
        (self.repo / "dir" / "a.txt").write_text("a\n")
        (self.repo / ".gitignore").write_text("ignored.log\n")
        sh(self.repo, "git", "add", "tracked.txt", "dir/a.txt", ".gitignore")
        sh(self.repo, "git", "commit", "-q", "-m", "init")
        # Isolate from the caller's TMPDIR.
        self._env = os.environ.get("TMPDIR")
        os.environ.pop("TMPDIR", None)

    def tearDown(self):
        if self._env is not None:
            os.environ["TMPDIR"] = self._env
        self._tmp.cleanup()

    def run_hook(self, payload):
        out = io.StringIO()
        with redirect_stdout(out), unittest.mock.patch("sys.stdin", io.StringIO(json.dumps(payload))):
            guard.main()
        text = out.getvalue().strip()
        return json.loads(text) if text else None

    def bash(self, command, cwd=None):
        return self.run_hook({"tool_name": "Bash", "tool_input": {"command": command},
                              "cwd": str(cwd or self.repo)})

    def assertDenied(self, command, contains=None, cwd=None):
        res = self.bash(command, cwd)
        self.assertIsNotNone(res, f"expected deny: {command!r}")
        out = res["hookSpecificOutput"]
        self.assertEqual(out["permissionDecision"], "deny")
        self.assertEqual(out["hookEventName"], "PreToolUse")
        if contains:
            self.assertIn(contains, out["permissionDecisionReason"])

    def assertAllowed(self, command, cwd=None):
        res = self.bash(command, cwd)
        self.assertIsNone(res, f"expected no opinion for {command!r}, got {res}")


import unittest.mock  # noqa: E402


class GitAdd(Repo):
    def test_whole_tree_adds_denied(self):
        for c in ["git add -A", "git add --all", "git add .", "git add -Av", "git add ../",
                  "git add '*.py'", "git add :/", "cd sub && git add -A",
                  "rtk git add -A", "git -C . add --all"]:
            self.assertDenied(c)

    def test_explicit_adds_allowed(self):
        for c in ["git add tracked.txt", "git add dir/", "git add -u", "git add -p dir/a.txt",
                  "git add -- -after.txt", "git add --dry-run .", "git add notes-after.md"]:
            self.assertAllowed(c)

    def test_quoted_text_is_not_a_command(self):
        self.assertAllowed('echo "run git add -A later" > notes.md')
        self.assertAllowed("git commit -m 'never git add -A'")


class Commit(Repo):
    """No approval prompt on commit, and no `-a...` false positives."""

    def test_commit_forms_allowed(self):
        for c in ["git commit -m msg", "git commit -F /tmp/msg-after.txt",
                  "git commit --allow-empty -m x", "git commit -am 'fix'",
                  "git push -u origin fm/harness-after", "cd ~/x-after && git status"]:
            self.assertAllowed(c)

    @unittest.skipUnless(shutil.which("gitleaks"), "gitleaks not installed")
    def test_secret_in_staged_diff_denied(self):
        key = "AKIA" + "Z7QXJ3KD2LMN4PQ5"  # AWS-shaped, not a real key
        (self.repo / "config.py").write_text(f'aws_access_key_id = "{key}"\n')
        sh(self.repo, "git", "add", "config.py")
        self.assertDenied("git commit -m 'add config'", contains="config.py")
        self.assertDenied("git -C . commit -m x", contains="gitleaks")

    @unittest.skipUnless(shutil.which("gitleaks"), "gitleaks not installed")
    def test_commit_all_scans_unstaged_tracked(self):
        key = "AKIA" + "Z7QXJ3KD2LMN4PQ5"
        (self.repo / "tracked.txt").write_text(f'key = "{key}"\n')
        self.assertAllowed("git commit -m 'staged only'")  # nothing staged has it
        self.assertDenied("git commit -am 'all'", contains="tracked.txt")

    @unittest.skipUnless(shutil.which("gitleaks"), "gitleaks not installed")
    def test_add_and_commit_in_one_line(self):
        # The hook runs before `git add`, so the index can't show the secret yet.
        key = "AKIA" + "Z7QXJ3KD2LMN4PQ5"
        (self.repo / "creds.py").write_text(f'aws_access_key_id = "{key}"\n')
        self.assertDenied('git add creds.py && git commit -m "add creds"', contains="creds.py")
        self.assertDenied("git commit -m x creds.py", contains="creds.py")
        (self.repo / "tracked.txt").write_text(f'key = "{key}"\n')
        self.assertDenied("git add -u && git commit -m x", contains="tracked.txt")

    @unittest.skipUnless(shutil.which("gitleaks"), "gitleaks not installed")
    def test_clean_commit_allowed(self):
        (self.repo / "ok.txt").write_text("hello\n")
        sh(self.repo, "git", "add", "ok.txt")
        self.assertAllowed("git commit -m ok")
        self.assertAllowed("git add ok.txt && git commit -m ok")


class Rm(Repo):
    @unittest.skipUnless(os.path.isdir("/tmp") and os.access("/tmp", os.W_OK), "no writable /tmp")
    def test_tmp_allowed(self):
        d = tempfile.mkdtemp(dir="/tmp")
        try:
            self.assertAllowed(f"{RM} -rf {d}")
            self.assertAllowed(f"{RM} -f /tmp/does-not-exist-piste-guard")
        finally:
            shutil.rmtree(d, ignore_errors=True)

    def test_clean_tracked_allowed(self):
        self.assertAllowed(f"{RM} tracked.txt")
        self.assertAllowed(f"{RM} -r dir")
        self.assertAllowed(f"cd dir && {RM} a.txt")

    def test_absent_allowed(self):
        self.assertAllowed(f"{RM} -f nope.txt")

    def test_untracked_or_modified_denied(self):
        (self.repo / "new.txt").write_text("x\n")
        self.assertDenied(f"{RM} new.txt", contains="trash-put")
        (self.repo / "tracked.txt").write_text("changed\n")
        self.assertDenied(f"{RM} tracked.txt")
        (self.repo / "dir" / "extra.txt").write_text("x\n")
        self.assertDenied(f"{RM} -rf dir")

    def test_ignored_denied(self):
        (self.repo / "ignored.log").write_text("x\n")
        self.assertDenied(f"{RM} ignored.log")

    def test_repo_root_and_git_dir_denied(self):
        self.assertDenied(f"{RM} -rf {self.repo}")
        self.assertDenied(f"{RM} -rf .git")

    def test_outside_git_denied(self):
        outside = Path(self._tmp.name) / "loose.txt"
        outside.write_text("x\n")
        self.assertDenied(f"{RM} {outside}")

    def test_wrapped_and_chained(self):
        (self.repo / "new.txt").write_text("x\n")
        self.assertDenied(f"true && sudo {RM} new.txt")
        self.assertDenied(f"bash -c '{RM} new.txt'")
        self.assertDenied(f"ls | xargs {RM}")
        self.assertDenied(f"{RM} $UNSET_PISTE_GUARD_VAR/x")

    def test_trash_put_and_heredoc_allowed(self):
        (self.repo / "new.txt").write_text("x\n")
        self.assertAllowed("trash-put new.txt")
        self.assertAllowed(f"cat > script.sh <<'EOF'\n{RM} -rf new.txt\nEOF\nchmod +x script.sh")
        self.assertAllowed(f"echo '{RM} -rf /'")
        self.assertAllowed(f"git {RM} tracked.txt")


class Discard(Repo):
    def test_reset_hard(self):
        self.assertAllowed("git reset --hard")
        (self.repo / "tracked.txt").write_text("changed\n")
        self.assertDenied("git reset --hard HEAD~0", contains="tracked.txt")

    def test_checkout(self):
        self.assertAllowed("git checkout -b feature")
        self.assertAllowed("git checkout .")
        (self.repo / "tracked.txt").write_text("changed\n")
        self.assertDenied("git checkout .")
        self.assertDenied("git checkout -- tracked.txt")
        self.assertDenied("git checkout -f main")
        self.assertAllowed("git checkout -- dir/a.txt")
        self.assertAllowed("git checkout main")

    def test_restore(self):
        (self.repo / "tracked.txt").write_text("changed\n")
        self.assertDenied("git restore tracked.txt")
        self.assertAllowed("git restore --staged tracked.txt")
        self.assertAllowed("git restore dir/a.txt")

    def test_clean(self):
        self.assertAllowed("git clean -fd")
        (self.repo / "new.txt").write_text("x\n")
        self.assertDenied("git clean -fd", contains="new.txt")
        self.assertAllowed("git clean -n")

    def test_force_push(self):
        self.assertDenied("git push --force origin fm/x")
        self.assertDenied("git push -f")
        self.assertDenied("git push origin +fm/x")
        self.assertAllowed("git push --force-with-lease origin fm/x")
        self.assertAllowed("git push -u origin fm/x")

    def test_push_no_verify(self):
        # The pre-push hook is the leak gate: skipping it is denied.
        for c in ["git push --no-verify origin main", "git push --no-veri",
                  "git -C . push --no-verify"]:
            self.assertDenied(c, contains="leak gate")
        self.assertAllowed("git push origin main")
        self.assertAllowed("git push --verbose")
        self.assertAllowed("git commit --no-verify -m x")


class Env(Repo):
    def test_bash_reads_denied(self):
        for c in ["cat .env", "grep KEY .env.local", "head -n 3 app/.env", "rtk read .env",
                  "cat < .env", "bat prod.env", "sed -n 1p .env.production"]:
            self.assertDenied(c, contains="dotenv")

    def test_bash_other_allowed(self):
        for c in ["cat .env.example", "cat .env.sample", "ls -la .env", "git add .env.example",
                  "echo FOO=1 >> notes.txt", "cat .envrc.md", "git commit -m 'mention .env'",
                  "set -a; source .env; set +a; ./run.sh"]:
            self.assertAllowed(c)

    def test_read_tool(self):
        deny = self.run_hook({"tool_name": "Read", "tool_input": {"file_path": "/x/.env"}})
        self.assertEqual(deny["hookSpecificOutput"]["permissionDecision"], "deny")
        self.assertIsNone(self.run_hook({"tool_name": "Read", "tool_input": {"file_path": "/x/.env.example"}}))
        self.assertIsNone(self.run_hook({"tool_name": "Read", "tool_input": {"file_path": "/x/env.py"}}))

    def test_grep_tool(self):
        deny = self.run_hook({"tool_name": "Grep", "tool_input": {"pattern": "K", "path": "/x/.env"}})
        self.assertEqual(deny["hookSpecificOutput"]["permissionDecision"], "deny")
        deny = self.run_hook({"tool_name": "Grep", "tool_input": {"pattern": "K", "glob": ".env*"}})
        self.assertEqual(deny["hookSpecificOutput"]["permissionDecision"], "deny")
        self.assertIsNone(self.run_hook({"tool_name": "Grep", "tool_input": {"pattern": "K", "glob": "*.py"}}))


class Robustness(Repo):
    def test_fail_open_on_bad_input(self):
        out = io.StringIO()
        with redirect_stdout(out), unittest.mock.patch("sys.stdin", io.StringIO("not json")), \
                unittest.mock.patch("sys.stderr", io.StringIO()):
            self.assertEqual(guard.main(), 0)
        self.assertEqual(out.getvalue(), "")

    def test_disable_switch(self):
        (self.repo / "new.txt").write_text("x\n")
        with unittest.mock.patch.dict(os.environ, {"PISTE_GUARD_DISABLE": "1"}):
            self.assertAllowed(f"{RM} new.txt")

    def test_messages_say_piste_guard(self):
        self.assertDenied("git add -A", contains="piste-guard:")

    def test_other_tools_ignored(self):
        self.assertIsNone(self.run_hook({"tool_name": "Write", "tool_input": {"file_path": ".env"}}))
        self.assertIsNone(self.run_hook({"tool_name": "apply_patch", "tool_input": {"command": "x"}}))

    def test_unbalanced_quotes(self):
        self.assertAllowed("echo 'unterminated")


class MainCheckout(Repo):
    """guard-edit: no edit tools inside a main checkout on the protected list."""

    def setUp(self):
        super().setUp()
        self.root = os.path.realpath(self.repo)
        self.listfile = Path(self._tmp.name) / "protected-checkouts"
        self.write_list(self.repo)
        self._prot = os.environ.get("PISTE_GUARD_PROTECTED")
        os.environ["PISTE_GUARD_PROTECTED"] = str(self.listfile)

    def tearDown(self):
        if self._prot is None:
            os.environ.pop("PISTE_GUARD_PROTECTED", None)
        else:
            os.environ["PISTE_GUARD_PROTECTED"] = self._prot
        super().tearDown()

    def write_list(self, *roots):
        lines = ["# protected main checkouts", "hint: use a worktree of {root}"]
        self.listfile.write_text("\n".join(lines + [str(r) for r in roots]) + "\n")

    def edit(self, path, tool="Edit", key="file_path", cwd=None):
        return self.run_hook({"tool_name": tool, "tool_input": {key: str(path)},
                              "cwd": str(cwd or self.repo)})

    def assertEditDenied(self, res, root=None):
        self.assertIsNotNone(res, "expected deny")
        out = res["hookSpecificOutput"]
        self.assertEqual(out["permissionDecision"], "deny")
        self.assertIn(root or self.root, out["permissionDecisionReason"])

    def worktree(self):
        wt = Path(self._tmp.name) / "wt"
        sh(self.repo, "git", "worktree", "add", "-q", str(wt), "-b", "feat")
        return wt

    def test_edit_in_protected_main_denied(self):
        res = self.edit(self.repo / "tracked.txt")
        self.assertEditDenied(res)
        self.assertIn("use a worktree of " + self.root, res["hookSpecificOutput"]["permissionDecisionReason"])

    def test_relative_path_resolved_against_cwd(self):
        self.assertEditDenied(self.edit("tracked.txt", cwd=self.repo))

    def test_write_new_file_in_new_dir_denied(self):
        self.assertEditDenied(self.edit(self.repo / "new" / "dir" / "x.txt", tool="Write"))

    def test_notebook_path_used(self):
        self.assertEditDenied(self.edit(self.repo / "nb.ipynb", tool="NotebookEdit", key="notebook_path"))

    def test_linked_worktree_allowed(self):
        wt = self.worktree()
        self.assertIsNone(self.edit(wt / "tracked.txt", cwd=wt))

    def test_symlink_into_main_denied(self):
        wt = self.worktree()
        (wt / "link.txt").symlink_to(self.repo / "tracked.txt")
        self.assertEditDenied(self.edit(wt / "link.txt", cwd=wt))

    def test_nested_repo_judged_by_own_entry(self):
        nested = self.repo / "nested"
        nested.mkdir()
        sh(nested, "git", "init", "-q", "-b", "main")
        self.assertIsNone(self.edit(nested / "f.txt"))
        self.write_list(nested)
        self.assertEditDenied(self.edit(nested / "f.txt"), root=os.path.realpath(nested))

    def test_missing_list_and_unlisted_repo_allowed(self):
        other = Path(self._tmp.name) / "other"
        other.mkdir()
        sh(other, "git", "init", "-q", "-b", "main")
        self.assertIsNone(self.edit(other / "f.txt", cwd=other))
        os.environ["PISTE_GUARD_PROTECTED"] = str(Path(self._tmp.name) / "absent")
        self.assertIsNone(self.edit(self.repo / "tracked.txt"))

    def test_bash_unaffected(self):
        self.assertAllowed("echo hi > tracked.txt")


if __name__ == "__main__":
    unittest.main()
