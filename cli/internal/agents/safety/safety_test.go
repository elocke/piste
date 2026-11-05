// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/utils/control-characters.test.ts, src/utils/confusable-names.test.ts
// (cases transcribed). Copyright (c) 2024 dyoshikawa. MIT License; see cli/THIRD_PARTY_NOTICES.md.

package safety

import (
	"reflect"
	"testing"
)

func TestStripControl(t *testing.T) {
	if got := StripControl("a\x1b[31mb‮c⁦d"); got != "a[31mbcd" {
		t.Fatalf("%q", got)
	}
	if got := StripHidden("pa​seo"); got != "paseo" {
		t.Fatalf("%q", got)
	}
}

func TestHasDeceptiveHidden(t *testing.T) {
	cases := map[string]bool{
		"plain text\nwith lines": false,
		"zero​width":             true,
		"bidi‮evil":              true,
		"keycap 1️⃣":             false,
		"emoji 👨‍💻":              false,
		"latin a‍b":              true,
		"tag \U000E0041":         true,
		"बि‍न":                   false,
	}
	for in, want := range cases {
		if got := HasDeceptiveHidden(in); got != want {
			t.Errorf("HasDeceptiveHidden(%q) = %v", in, got)
		}
	}
}

func TestMixedScripts(t *testing.T) {
	if got := MixedScripts("pаseo"); !reflect.DeepEqual(got, []string{"Cyrillic", "Latin"}) {
		t.Fatal(got)
	}
	if MixedScripts("paseo-loop") != nil || MixedScripts("日本") != nil {
		t.Fatal("single script flagged")
	}
}

func TestConfusable(t *testing.T) {
	got := Confusable([]string{"paseo", "pаseo", "notes-clip", "notes_clip", "modern", "modem", "other"})
	want := [][]string{{"modem", "modern"}, {"notes-clip", "notes_clip"}, {"paseo", "pаseo"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestLooksSecret(t *testing.T) {
	yes := [][2]string{
		{"Authorization", "Bearer abcdefghijklmnop"},
		{"GITHUB_TOKEN", "ghp_abcdefghijklmnopqrstuvwxyz0123"},
		{"x", "sk-proj-abcdefghijklmnopqrst"},
		{"API_KEY", "hunter2hunter2"},
		{"url", "https://user:pass@host/x"},
		{"url", "https://h/mcp?token=abcdef123456"}, // gitleaks:allow
	}
	no := [][2]string{
		{"Authorization", "Bearer ${DATA_FEED_TOKEN}"},
		{"GITHUB_TOKEN", "${GITHUB_TOKEN}"},
		{"LOG_LEVEL", "debug"},
		{"url", "https://h/mcp"},
		{"HINDSIGHT_MCP_HARNESS", "claude"},
		{"TOKEN_FILE", ""},
	}
	for _, c := range yes {
		if !LooksSecret(c[0], c[1]) {
			t.Errorf("missed %v", c)
		}
	}
	for _, c := range no {
		if LooksSecret(c[0], c[1]) {
			t.Errorf("false positive %v", c)
		}
	}
}

func TestRedact(t *testing.T) {
	v, name, ok := Redact("data-feed", "Authorization", "Bearer abcdefghijklmnop")
	if !ok || v != "Bearer ${DATA_FEED_TOKEN}" || name != "DATA_FEED_TOKEN" {
		t.Fatal(v, name, ok)
	}
	v, name, ok = Redact("gh", "GITHUB_TOKEN", "ghp_abcdefghijklmnopqrstuvwxyz0123")
	if !ok || v != "${GH_GITHUB_TOKEN}" || name != "GH_GITHUB_TOKEN" {
		t.Fatal(v, name)
	}
	if _, _, ok := Redact("x", "LOG_LEVEL", "debug"); ok {
		t.Fatal("redacted a non-secret")
	}
}
