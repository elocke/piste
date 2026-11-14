package cli

// The record of the last piste switch, for piste fix. When piste fix's
// rebuild passes, the switch failed in activation or in the check after it,
// and only the switch's own output says how: so every switch keeps it in
// ${XDG_STATE_HOME:-~/.local/state}/piste/switch.log (switchLog), on every
// platform, overwritten by the next one. A header names the host and the
// flake; a footer, written when the switch ends, its exit code.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elocke/piste/cli/internal/platform"
)

// switchRecord is the switch's log while it runs. tee: piste's messages and
// the switch's own output are copied into it; off in a detached switch's
// unit, whose output systemd already appends to the same file, so only the
// header and the footer are written there.
type switchRecord struct {
	mu  sync.Mutex
	f   *os.File
	tee bool
}

func (r *switchRecord) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Write(p)
}

// line writes one header or footer line.
func (r *switchRecord) line(format string, args ...any) {
	fmt.Fprintf(r, format+"\n", args...)
}

// teeing is the record when messages and output go into it, else nil.
func (a *App) teeing() io.Writer {
	if a.record != nil && a.record.tee {
		return a.record
	}
	return nil
}

// inSwitchUnit: this piste is a detached switch's own unit (switchdetach.go),
// from its cgroup file: its output is the log already.
func inSwitchUnit(cgroup string) bool {
	for _, line := range strings.Split(cgroup, "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			return strings.Contains(path, "/piste-switch-")
		}
	}
	return false
}

func runningInSwitchUnit() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	b, err := os.ReadFile(platform.CgroupFile)
	return err == nil && inSwitchUnit(string(b))
}

// startRecord opens the switch's log and writes its header. In a detached
// switch's unit the file is the unit's output (appended to, emptied by the
// piste that started it); elsewhere it is emptied here, only the person's
// (0600). A log that can't be opened is a warning: the switch goes on.
func (a *App) startRecord(home string) {
	path := switchLog(a.Env.Getenv, home)
	detached := runningInSwitchUnit()
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if detached {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		a.warn("Can't keep this switch's log: " + err.Error())
		return
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		a.warn("Can't keep this switch's log: " + err.Error())
		return
	}
	_ = f.Chmod(0o600)
	a.record = &switchRecord{f: f, tee: !detached}
	a.record.line("# piste switch: %s, piste %s", time.Now().UTC().Format(time.RFC3339), Version)
	a.record.line("# ran: %s", shellJoin(append([]string{"piste"}, a.Args...)))
}

// recordTarget writes the host and the flake the switch acts on.
func (a *App) recordTarget(host, flakeDir string) {
	if a.record != nil {
		a.record.line("# host: %s", host)
		a.record.line("# flake: %s", flakeDir)
	}
}

// endRecord writes the footer, the switch's exit code, and closes the log.
func (a *App) endRecord(err error) {
	if a.record == nil {
		return
	}
	code := 0
	var ee ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.Code
	case err != nil:
		code = 1
	}
	a.record.line("# exit: %d", code)
	a.record.f.Close()
	a.record = nil
}

// lastSwitch is what a switch's log says: its host and flake, when it ran,
// whether it ended (a footer) and how, and its body.
type lastSwitch struct {
	When     string
	Host     string
	Flake    string
	Finished bool
	Exit     int
	Body     []string
}

// failed: the switch ended non-zero, or never ended (killed part-way).
func (s lastSwitch) failed() bool { return !s.Finished || s.Exit != 0 }

// readSwitchLog reads the log at path; ok is false when there's none, or
// none with a header.
func readSwitchLog(path string) (lastSwitch, bool) {
	f, err := os.Open(path)
	if err != nil {
		return lastSwitch{}, false
	}
	defer f.Close()
	var s lastSwitch
	header := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "# piste switch: "):
			header = true
			s.When, _, _ = strings.Cut(strings.TrimPrefix(l, "# piste switch: "), ",")
		case strings.HasPrefix(l, "# host: "):
			s.Host = strings.TrimPrefix(l, "# host: ")
		case strings.HasPrefix(l, "# flake: "):
			s.Flake = strings.TrimPrefix(l, "# flake: ")
		case strings.HasPrefix(l, "# exit: "):
			if n, err := strconv.Atoi(strings.TrimPrefix(l, "# exit: ")); err == nil {
				s.Finished, s.Exit = true, n
			}
		case strings.HasPrefix(l, "# ran: "):
		default:
			s.Body = append(s.Body, l)
		}
	}
	return s, header
}

var (
	ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	// switchErrLine: a line that says what went wrong, from nix, an
	// activation script, Homebrew, launchd or piste's check.
	switchErrLine = regexp.MustCompile(`(?i)\berror\b|\bfailed\b|fails the check|\bUndone:|abort|refusing|cannot|can't|not found|permission denied`)
	// switchHints: piste's own advice after a failure, which says nothing
	// about the cause.
	switchHints = regexp.MustCompile(`^(To rollback:|To debug:|To have .* fix it:)`)
)

const (
	excerptLines = 10
	excerptWidth = 240
)

// excerpt is the lines of a failed switch's body that say what went wrong:
// the last excerptLines lines that read as an error (escape codes out, each
// cut to excerptWidth), else its last lines. piste's own hints are left out.
func (s lastSwitch) excerpt() []string {
	var body []string
	for _, l := range s.Body {
		l = strings.TrimRight(ansiEscape.ReplaceAllString(l, ""), " \t\r")
		if i := strings.LastIndexByte(l, '\r'); i >= 0 {
			l = l[i+1:]
		}
		if strings.TrimSpace(l) == "" || switchHints.MatchString(strings.TrimSpace(l)) {
			continue
		}
		body = append(body, l)
	}
	var errs []string
	for _, l := range body {
		if switchErrLine.MatchString(l) && (len(errs) == 0 || errs[len(errs)-1] != l) {
			errs = append(errs, l)
		}
	}
	if len(errs) == 0 {
		errs = body
	}
	if len(errs) > excerptLines {
		errs = errs[len(errs)-excerptLines:]
	}
	out := make([]string, len(errs))
	for i, l := range errs {
		if len(l) > excerptWidth {
			l = l[:excerptWidth] + "…"
		}
		out[i] = l
	}
	return out
}

// failedSwitch is the last switch's log when it is of host from flakeDir and
// it failed; ok false otherwise (none, another machine's or flake's, or one
// that passed).
func failedSwitch(path, host, flakeDir string) (lastSwitch, bool) {
	s, ok := readSwitchLog(path)
	if !ok || s.Host != host || s.Flake != flakeDir || !s.failed() {
		return lastSwitch{}, false
	}
	return s, true
}
