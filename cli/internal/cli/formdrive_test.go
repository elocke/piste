package cli

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/cursor"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// Forms in tests: keys go through the form's own Update one at a time, and
// what each returns is run before the next key, so nothing races (feeding
// the same keys as bytes through WithInput does).

var (
	enter    = tea.KeyPressMsg{Code: tea.KeyEnter}
	up       = tea.KeyPressMsg{Code: tea.KeyUp}
	down     = tea.KeyPressMsg{Code: tea.KeyDown}
	left     = tea.KeyPressMsg{Code: tea.KeyLeft}
	right    = tea.KeyPressMsg{Code: tea.KeyRight}
	shiftTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	ctrlC    = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	ctrlU    = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
)

// text types s.
func text(s string) []tea.KeyPressMsg {
	var ks []tea.KeyPressMsg
	for _, r := range s {
		ks = append(ks, tea.KeyPressMsg{Text: string(r), Code: r})
	}
	return ks
}

// blinks: the cursor's blink, a command that waits out its blink interval
// (the only command huh's fields give that doesn't return at once).
func blinks(cmd tea.Cmd) bool {
	fn := runtime.FuncForPC(reflect.ValueOf(cmd).Pointer())
	return fn != nil && strings.Contains(fn.Name(), "/cursor.(*Model).Blink")
}

// settle runs cmd and feeds what it returns back into f until nothing is
// left, skipping the cursor's blink.
func settle(f *huh.Form, cmd tea.Cmd, depth int) {
	if cmd == nil || depth > 50 || blinks(cmd) {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(10 * time.Second):
		panic("settle: a command didn't return: " + runtime.FuncForPC(reflect.ValueOf(cmd).Pointer()).Name())
	}
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			settle(f, c, depth+1)
		}
	default:
		if reflect.TypeOf(msg).String() == "tea.sequenceMsg" {
			v := reflect.ValueOf(msg)
			for i := 0; i < v.Len(); i++ {
				settle(f, v.Index(i).Interface().(tea.Cmd), depth+1)
			}
			return
		}
		_, next := f.Update(msg)
		settle(f, next, depth+1)
	}
}

// key gives f one key, through the same guard as on the terminal.
func key(f *huh.Form, k tea.KeyPressMsg) {
	if guardBack(f, k) == nil {
		return
	}
	_, cmd := f.Update(k)
	settle(f, cmd, 0)
}

// press starts f in a 100x40 terminal and gives it steps while it is open:
// a key, a run of keys, or a func(*huh.Form) that checks the form at that
// point.
func press(f *huh.Form, steps ...any) {
	settle(f, f.Init(), 0)
	_, cmd := f.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	settle(f, cmd, 0)
	for _, s := range steps {
		if f.State != huh.StateNormal {
			return
		}
		switch v := s.(type) {
		case tea.KeyPressMsg:
			key(f, v)
		case []tea.KeyPressMsg:
			for _, k := range v {
				if f.State != huh.StateNormal {
					return
				}
				key(f, k)
			}
		case func(*huh.Form):
			v(f)
		default:
			panic("press: a key, keys or a func(*huh.Form)")
		}
	}
}

// driver is an App.runForm for tests: each form run takes the next run's
// steps, which must finish the form; n counts the runs.
type driver struct {
	t    *testing.T
	runs [][]any
	n    int
}

func drive(t *testing.T, runs ...[]any) *driver { return &driver{t: t, runs: runs} }

func (d *driver) run(f *huh.Form) error {
	d.t.Helper()
	if d.n >= len(d.runs) {
		d.t.Fatalf("form run %d: the test gave %d", d.n+1, len(d.runs))
	}
	press(f, d.runs[d.n]...)
	d.n++
	switch f.State {
	case huh.StateCompleted:
		return nil
	case huh.StateAborted:
		return huh.ErrUserAborted
	}
	d.t.Fatalf("form run %d: still open after its steps", d.n)
	return errors.New("unreachable")
}

func ptr[T any](v T) *T { return &v }

// The driver skips the cursor's blink (it waits out its interval) and
// nothing else.
func TestDriverSkipsOnlyTheBlink(t *testing.T) {
	c := cursor.New()
	c.SetMode(cursor.CursorBlink)
	if !blinks(c.Focus()) {
		t.Error("the cursor's blink not recognised")
	}
	if blinks(func() tea.Msg { return nil }) || blinks(tea.Batch(func() tea.Msg { return nil }, func() tea.Msg { return nil })) {
		t.Error("an ordinary command taken for the blink")
	}
}
