// Package execx is the one place piste runs external programs.
//
// Every call has an explicit argv (never a shell), an optional timeout, and
// goes through a Runner, so tests substitute shims and --dry-run records
// instead of running.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Cmd is one external invocation.
type Cmd struct {
	Argv    []string      // Argv[0] is looked up on PATH
	Dir     string        // working directory; "" = current
	Env     []string      // extra KEY=VALUE pairs on top of the process environment
	Stdin   io.Reader     // nil = no stdin
	Timeout time.Duration // 0 = none
	// Passthrough inherits the terminal (stdin/stdout/stderr) instead of
	// capturing, for darwin-rebuild, sudo prompts and $EDITOR.
	Passthrough bool
}

// Result is what a captured run produced.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner executes commands. Real runs use Exec; tests use a Recorder or shims.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// ErrExit is returned (wrapped) when the program ran but exited non-zero.
var ErrExit = errors.New("non-zero exit")

// Exec runs commands for real.
type Exec struct {
	Verbose io.Writer // when set, each argv is echoed here before running
}

func (e Exec) Run(ctx context.Context, c Cmd) (Result, error) {
	if len(c.Argv) == 0 {
		return Result{}, errors.New("execx: empty argv")
	}
	if e.Verbose != nil {
		fmt.Fprintf(e.Verbose, "+ %s\n", Quote(c.Argv))
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	var out, errb bytes.Buffer
	if c.Passthrough {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	} else {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, &out, &errb
	}
	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	if err == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		if ctx.Err() == context.DeadlineExceeded {
			return res, fmt.Errorf("%s: timed out after %s", c.Argv[0], c.Timeout)
		}
		return res, fmt.Errorf("%s: %w (exit %d)", c.Argv[0], ErrExit, res.ExitCode)
	}
	return res, fmt.Errorf("%s: %w", c.Argv[0], err)
}

// Recorder records argv instead of running (--dry-run, unit tests). Replies
// maps a space-joined argv prefix to a canned result; the longest match wins.
type Recorder struct {
	mu      sync.Mutex
	Calls   [][]string
	Replies map[string]Result
}

func (r *Recorder) Run(_ context.Context, c Cmd) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Calls = append(r.Calls, append([]string(nil), c.Argv...))
	joined := strings.Join(c.Argv, " ")
	best := ""
	for k := range r.Replies {
		if strings.HasPrefix(joined, k) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return Result{}, nil
	}
	res := r.Replies[best]
	if res.ExitCode != 0 {
		return res, fmt.Errorf("%s: %w (exit %d)", c.Argv[0], ErrExit, res.ExitCode)
	}
	return res, nil
}

// Quote renders an argv for display (not for a shell).
func Quote(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n'\"\\$`") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
