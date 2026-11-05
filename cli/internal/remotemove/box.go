package remotemove

// The box side. Every remote step is a bash script sent on ssh's stdin to
// `bash -s`, its values single-quoted in; paths are relative to the box's
// $HOME (the scripts prefix it), so no remote home is assumed.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/elocke/piste/cli/internal/execx"
)

// shQuote single-quotes s for a POSIX shell.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

var shSafe = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`)

// shWord leaves a plain word bare and quotes anything else.
func shWord(s string) string {
	if shSafe.MatchString(s) {
		return s
	}
	return shQuote(s)
}

// homePath is a shell word for $HOME/rel.
func homePath(rel string) string { return `"$HOME"/` + shQuote(rel) }

// ResumeCommand is what to run here to pick the session up on the box.
func ResumeCommand(host, relCwd, session string) string {
	return "ssh -t " + shWord(host) + " " + shQuote("cd ~/"+shWord(relCwd)+" && claude --resume "+shWord(session))
}

func (m *mover) sshArgv(remote ...string) []string {
	argv := append([]string{"ssh"}, SSHOptions...)
	return append(append(argv, m.o.Host, "--"), remote...)
}

// ssh runs script on the box and returns its stdout.
func (m *mover) ssh(ctx context.Context, script string) (string, error) {
	res, err := m.o.Runner.Run(ctx, execx.Cmd{Argv: m.sshArgv("bash", "-s"), Stdin: strings.NewReader(script)})
	return string(res.Stdout), withStderr(err, res.Stderr)
}

// pushArgv pushes sha from the local checkout into the box clone (git's
// scp-like host:path is relative to the box's home). Never origin.
func pushArgv(top, host, relMain, sha, branch string) []string {
	return []string{"git", "-C", top,
		"-c", "core.sshCommand=ssh " + strings.Join(SSHOptions, " "),
		"push", "--quiet", host + ":" + relMain, sha + ":" + PushRef + branch}
}

// copyCommand writes ssh's stdin (the transcript) to the staged path.
func copyCommand(staged string) string { return "cat > " + homePath(staged) }

const header = "set -euo pipefail\n"

// alreadyScript prints staged's import marker if the session was already
// moved.
func alreadyScript(staged string) string {
	return header + fmt.Sprintf("result=%s\n", homePath(staged+".result.json")) + `
if [ -s "$result" ]; then cat "$result"; fi
`
}

// prepareScript clones origin at main (or refreshes origin's refs if it
// exists) and creates the staging dir.
func prepareScript(relMain, origin string) string {
	return header + fmt.Sprintf("main=%s origin=%s stage=%s\n", homePath(relMain), shQuote(origin), homePath(Stage)) + `
mkdir -p "$stage"
if [ -e "$main/.git" ]; then
  git -C "$main" fetch --quiet origin
else
  mkdir -p "$(dirname "$main")"
  git clone --quiet "$origin" "$main"
fi
`
}

// checkoutScript checks branch out at co (a worktree of main, or main
// itself) and fast-forwards it to sha, which was pushed to PushRef<branch>.
// A box branch that moved on its own is left alone (the fast-forward fails).
func checkoutScript(relMain, relTop, branch, sha string) string {
	return header + fmt.Sprintf("main=%s co=%s br=%s sha=%s ref=%s\n",
		homePath(relMain), homePath(relTop), shQuote(branch), shQuote(sha), shQuote(PushRef+branch)) + `
git -C "$main" show-ref --verify --quiet "refs/heads/$br" \
  || git -C "$main" branch --quiet --no-track "$br" "$ref"
if [ "$co" != "$main" ]; then
  [ -e "$co" ] || git -C "$main" worktree add --quiet "$co" "$br"
elif [ "$(git -C "$main" symbolic-ref --short -q HEAD)" != "$br" ]; then
  git -C "$main" switch --quiet "$br"
fi
if [ "$(git -C "$co" symbolic-ref --short -q HEAD)" != "$br" ]; then
  echo "$co exists but is not on $br" >&2
  exit 1
fi
[ "$(git -C "$co" rev-parse HEAD)" = "$sha" ] || git -C "$co" merge --quiet --ff-only "$sha"
if git -C "$main" show-ref --verify --quiet "refs/remotes/origin/$br"; then
  git -C "$main" branch --quiet --set-upstream-to="origin/$br" "$br"
fi
`
}

// importScript imports the staged transcript as a new box session and
// prints session-migrate's result JSON (an existing marker wins). The
// marker is only committed by finishScript, once piste has checked the result.
func importScript(staged, relCwd string) string {
	return header + fmt.Sprintf("staged=%s cwd=%s smigrate=%s\n", homePath(staged), homePath(relCwd), shQuote(SessionMigrate)) + `
result="$staged.result.json"
if [ -s "$result" ]; then cat "$result"; exit 0; fi
cd "$cwd"
uvx --quiet --from "$smigrate" session-migrate import "$staged" --to claude --cwd "$cwd" >"$result.tmp"
cat "$result.tmp"
`
}

// finishScript saves the checked result as the marker and drops the staged
// transcript.
func finishScript(staged string) string {
	return header + fmt.Sprintf("staged=%s\n", homePath(staged)) + `
result="$staged.result.json"
[ -s "$result" ] || mv "$result.tmp" "$result"
rm -f "$staged" "$result.tmp"
`
}
