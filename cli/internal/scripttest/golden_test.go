package scripttest

import (
	"reflect"
	"testing"
)

func set(names ...string) func(string) bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return func(n string) bool { return m[n] }
}

func TestVariantForPrefersThisOS(t *testing.T) {
	has := set("g/a", "g/a@darwin", "g/b", "g/c@linux")
	for _, c := range []struct{ path, goos, want string }{
		{"g/a", "darwin", "g/a@darwin"},
		{"g/a", "linux", "g/a"},
		{"g/b", "darwin", "g/b"},
		{"g/c", "linux", "g/c@linux"},
	} {
		if got := variantFor(c.path, c.goos, has); got != c.want {
			t.Errorf("variantFor(%q, %q) = %q, want %q", c.path, c.goos, got, c.want)
		}
	}
}

func TestExpectedNamesFoldsVariants(t *testing.T) {
	files := []string{"a", "a@darwin", "b@linux", "c@darwin", "c@linux", "d"}
	has := set(files...)
	// darwin: b has only linux's variant, so a darwin run makes no b.
	if got, want := expectedNames(files, "darwin", has), []string{"a", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("darwin: got %v, want %v", got, want)
	}
	if got, want := expectedNames(files, "linux", has), []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("linux: got %v, want %v", got, want)
	}
}

func TestFirstDiffCatchesTrailingLines(t *testing.T) {
	for _, c := range []struct {
		got, want string
		differs   bool
		line      int
	}{
		{"a\n", "a\n", false, 0},
		{"a\nb\n", "a\nc\n", true, 2},
		{"a\n\n", "a\n", true, 3},
		{"", "\n", true, 2},
		{"a", "a\n", true, 2},
	} {
		line, _, _, differs := firstDiff(c.got, c.want)
		if differs != c.differs || line != c.line {
			t.Errorf("firstDiff(%q, %q) = line %d, differs %v; want line %d, %v", c.got, c.want, line, differs, c.line, c.differs)
		}
	}
}

func TestUpdatePlanLinuxIsCanonical(t *testing.T) {
	files := map[string]string{"g/a": "old", "g/a@linux": "stale", "g/a@darwin": "mac"}
	read := func(p string) (string, bool) { s, ok := files[p]; return s, ok }
	for _, c := range []struct {
		path, got, goos, write string
		remove                 []string
	}{
		// Linux always writes the base and drops a stale @linux.
		{"g/a", "new", "linux", "g/a", []string{"g/a@linux"}},
		{"g/n", "new", "linux", "g/n", nil},
		// darwin writes @darwin only where it differs from the base.
		{"g/a", "old", "darwin", "", []string{"g/a@darwin"}},
		{"g/a", "other", "darwin", "g/a@darwin", nil},
		{"g/n", "new", "darwin", "g/n@darwin", nil},
	} {
		write, remove := updatePlan(c.path, c.got, c.goos, read)
		if write != c.write || !reflect.DeepEqual(remove, c.remove) {
			t.Errorf("updatePlan(%q, %q, %s) = %q, %v; want %q, %v", c.path, c.got, c.goos, write, remove, c.write, c.remove)
		}
	}
}

func TestStaleGoldens(t *testing.T) {
	files := []string{"a", "a@darwin", "b", "b@linux", "b@darwin", "c@darwin", "d"}
	outputs := map[string]bool{"a": true, "d": true}
	if got, want := staleGoldens(files, outputs, "linux"), []string{"b", "b@linux"}; !reflect.DeepEqual(got, want) {
		t.Errorf("linux: got %v, want %v", got, want)
	}
	if got, want := staleGoldens(files, outputs, "darwin"), []string{"b@darwin", "c@darwin"}; !reflect.DeepEqual(got, want) {
		t.Errorf("darwin: got %v, want %v", got, want)
	}
}
