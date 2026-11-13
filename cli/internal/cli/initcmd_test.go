package cli

import (
	"bufio"
	"errors"
	"io"
	"testing"
)

// The home a Linux host's file gets: this machine's only when this machine
// is Linux too (an init on a Mac for a Linux box isn't /Users/<user>).
func TestLinuxHome(t *testing.T) {
	for _, c := range []struct{ user, me, home, here, want string }{
		{"jane", "jane", "/var/home/jane", "x86_64-linux", "/var/home/jane"},
		{"jane", "jane", "/Users/jane", "aarch64-darwin", "/home/jane"},
		{"bob", "jane", "/home/jane", "x86_64-linux", "/home/bob"},
	} {
		if got := linuxHome(c.user, c.me, c.home, c.here); got != c.want {
			t.Errorf("linuxHome(%q, %q, %q, %q) = %q, want %q", c.user, c.me, c.home, c.here, got, c.want)
		}
	}
}

// eofOnce is a terminal where Ctrl-D ends one read and typing goes on.
type eofOnce struct{ reads []string }

func (r *eofOnce) Read(p []byte) (int, error) {
	if len(r.reads) == 0 {
		return 0, io.EOF
	}
	s := r.reads[0]
	r.reads = r.reads[1:]
	if s == "" {
		return 0, io.EOF
	}
	return copy(p, s), nil
}

// Ctrl-D at one question takes its default; a typo at a later one is
// still asked again.
func TestAskCheckAfterCtrlD(t *testing.T) {
	p := &prompter{in: bufio.NewReader(&eofOnce{reads: []string{"", "my mac\n", "devbox\n"}}), out: io.Discard}
	if got := p.ask("User", "jane"); got != "jane" {
		t.Fatalf("ask = %q, want the default", got)
	}
	got, err := p.askCheck("Hostname", "box", func(h string) error {
		if h == "my mac" {
			return errors.New("bad")
		}
		return nil
	})
	if got != "devbox" || err != nil {
		t.Errorf("askCheck = %q, %v; want devbox", got, err)
	}
}
