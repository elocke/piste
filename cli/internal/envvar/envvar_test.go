package envvar

import "testing"

func lookup(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestGetReadsPiste(t *testing.T) {
	if got := Get(lookup(map[string]string{"PISTE_X": "a"}), "X"); got != "a" {
		t.Fatalf("Get = %q, want a", got)
	}
}

// Only PISTE_ names are read.
func TestGetIgnoresOtherPrefixes(t *testing.T) {
	if got := Get(lookup(map[string]string{"OTHER_X": "b"}), "X"); got != "" {
		t.Fatalf("Get = %q, want empty", got)
	}
}
