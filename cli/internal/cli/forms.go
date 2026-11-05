package cli

import (
	"errors"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"
)

// Questions at a terminal are Charm forms (huh); anywhere else (a pipe, CI,
// the end-to-end tests, a screen reader) they are the plain one-line prompts,
// unchanged. huh picks its light or dark colours from the terminal itself.

// errStopped: the person quit a form (Ctrl+C).
var errStopped = errors.New("stopped at a question")

// formsWanted: a person at a terminal (one that reports its width: huh
// can't lay a form out in none) who hasn't asked for plain output
// (NO_COLOR, a dumb terminal, or ACCESSIBLE for a screen reader).
func formsWanted(terminal bool, cols int, getenv func(string) string) bool {
	return terminal && cols > 0 && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb" && getenv("ACCESSIBLE") == ""
}

// useForms is formsWanted for this run, decided once so a command never
// mixes the two styles.
func (a *App) useForms() bool {
	if a.forms == nil {
		getenv := os.Getenv
		if a.Env.Getenv != nil {
			getenv = a.Env.Getenv
		}
		terminal, cols := a.Stdin == io.Reader(os.Stdin) && interactive(a.Stdout), 0
		if terminal {
			cols, _, _ = term.GetSize(a.Stdout.(*os.File).Fd())
		}
		a.forms = ptrTo(formsWanted(terminal, cols, getenv))
	}
	return *a.forms
}

func ptrTo[T any](v T) *T { return &v }

// form runs f on the terminal (a.runForm in tests): errStopped when the
// person quit it.
func (a *App) form(f *huh.Form) error {
	f = f.WithTheme(huh.ThemeFunc(pisteTheme))
	run := a.runForm
	if run == nil {
		run = func(f *huh.Form) error {
			guard := tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg { return guardBack(f, msg) })
			return f.WithProgramOptions(guard).WithInput(a.Stdin).WithOutput(a.Stdout).Run()
		}
	}
	if err := run(f); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return errStopped
		}
		return err
	}
	return nil
}

// guardBack keeps Shift+Tab on a field whose answer is refused: huh checks
// the field it leaves, and off a screen's first field a refusal leaves the
// form with nothing focused, deaf to every key but Ctrl+C. The answer's
// error shows instead, and the field keeps the keys.
func guardBack(f *huh.Form, msg tea.Msg) tea.Msg {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || k.String() != "shift+tab" {
		return msg
	}
	fld := f.GetFocusedField()
	if fld == nil {
		return msg
	}
	fld.Blur()
	err := fld.Error()
	fld.Focus()
	if err != nil {
		return nil
	}
	return msg
}

// pisteTheme is huh's Charm theme with options, buttons and placeholders in
// the terminal's own colours: huh never asks the terminal whether its
// background is dark, and on a light one its fixed greys (252, 235) are
// near-invisible.
func pisteTheme(isDark bool) *huh.Styles {
	t := huh.ThemeCharm(isDark)
	for _, fs := range []*huh.FieldStyles{&t.Focused, &t.Blurred} {
		fs.Option = fs.Option.UnsetForeground()
		fs.UnselectedOption = fs.UnselectedOption.UnsetForeground()
		fs.BlurredButton = fs.BlurredButton.UnsetForeground().UnsetBackground().Faint(true)
		fs.TextInput.Placeholder = fs.TextInput.Placeholder.UnsetForeground().Faint(true)
	}
	return t
}

// askYesNo is one yes/no question, def selected.
func (a *App) askYesNo(title, yes, no string, def bool) (bool, error) {
	v := def
	err := a.form(huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Affirmative(yes).Negative(no).Value(&v),
	)))
	return v, err
}

// pickOne is one choice of opts, def selected ("/" filters them).
func (a *App) pickOne(title string, opts []huh.Option[string], def string) (string, error) {
	v := def
	err := a.form(huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title(title).Options(opts...).Value(&v),
	)))
	return v, err
}
