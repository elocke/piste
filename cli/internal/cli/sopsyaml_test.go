package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	pubA = "age1alphaalphaalphaalpha"
	pubB = "age1bravobravobravobravo"
	pubC = "age1charliecharliecharlie"
)

// userSopsYAML is the .sops.yaml piste init --sops wrote before it anchored
// the key by the machine's name (&user): overlays made then still have it.
const userSopsYAML = "keys:\n" +
	"  - &user " + pubA + "\n" +
	"creation_rules:\n" +
	"  - path_regex: secrets/.*\\.sops\\.yaml$\n" +
	"    key_groups:\n" +
	"      - age:\n" +
	"          - *user\n"

func writeSopsYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".sops.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readString(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// init anchors its key by the machine's name, as host add anchors the next
// one's: in .sops.yaml a key to remove is found by its machine.
func TestEnsureSopsAnchorsByHost(t *testing.T) {
	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out}
	home, dir := t.TempDir(), t.TempDir()
	if err := a.ensureSops(home, dir, "jane-mac"); err != nil {
		t.Fatal(err)
	}
	pub := agePubkey(ageKeyPath(home))
	if got, want := readString(t, filepath.Join(dir, ".sops.yaml")), initSopsYAML("jane-mac", pub); got != want || !strings.Contains(got, "&jane-mac "+pub) {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if !strings.Contains(out.String(), "password manager") {
		t.Errorf("no keep-a-copy line after the new key:\n%s", out.String())
	}
	p := filepath.Join(dir, ".sops.yaml")
	if anchor, _, err := addRecipient(p, "devbox", pubB); err != nil || anchor != "devbox" {
		t.Fatalf("host add after init: %q, %v", anchor, err)
	}
	for in, want := range map[string]string{"jane-mac": "jane-mac", "box.lan": "box.lan", "my mac": "my-mac", "": "machine", "a{b}": "a-b-"} {
		if got := sopsAnchor(in); got != want {
			t.Errorf("sopsAnchor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAddRecipientInitShape(t *testing.T) {
	p := writeSopsYAML(t, userSopsYAML)
	anchor, changed, err := addRecipient(p, "devbox", pubB)
	if err != nil || anchor != "devbox" || !changed {
		t.Fatalf("addRecipient = %q, %v, %v", anchor, changed, err)
	}
	want := "keys:\n" +
		"  - &user " + pubA + "\n" +
		"  - &devbox " + pubB + "\n" +
		"creation_rules:\n" +
		"  - path_regex: secrets/.*\\.sops\\.yaml$\n" +
		"    key_groups:\n" +
		"      - age:\n" +
		"          - *user\n" +
		"          - *devbox\n"
	if got := readString(t, p); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want it kept (0600)", fi.Mode().Perm())
	}
}

// Only lines are added: comments, other rules and other lists stay byte for
// byte.
func TestAddRecipientKeepsTheRest(t *testing.T) {
	in := "# The overlay's sops rules.\n" +
		"keys:\n" +
		"  - &user " + pubA + " # the first Mac\n" +
		"  # a retired key was here\n" +
		"  - &box " + pubC + "\n" +
		"creation_rules:\n" +
		"  # env vars\n" +
		"  - path_regex: secrets/env/.*\\.sops\\.yaml$\n" +
		"    key_groups:\n" +
		"      - age:\n" +
		"          - *user\n" +
		"          - *box\n" +
		"        pgp:\n" +
		"          - 0123456789ABCDEF\n" +
		"  - path_regex: secrets/shared/.*\n" +
		"    shamir_threshold: 1\n" +
		"    key_groups:\n" +
		"    - age:\n" +
		"      - *user\n" +
		"    - age:\n" +
		"      - *box\n" +
		"# end\n"
	p := writeSopsYAML(t, in)
	if _, changed, err := addRecipient(p, "devbox", pubB); err != nil || !changed {
		t.Fatalf("addRecipient: %v, %v", changed, err)
	}
	got := readString(t, p)
	var kept, added []string
	for _, l := range strings.SplitAfter(got, "\n") {
		if strings.Contains(l, "devbox") {
			added = append(added, l)
		} else {
			kept = append(kept, l)
		}
	}
	if strings.Join(kept, "") != in {
		t.Errorf("the rest changed:\n%s", got)
	}
	want := []string{"  - &devbox " + pubB + "\n", "          - *devbox\n", "      - *devbox\n", "      - *devbox\n"}
	if strings.Join(added, "|") != strings.Join(want, "|") {
		t.Errorf("added %q, want %q", added, want)
	}
	// Each alias right after its list's last item.
	for _, after := range []string{"          - *box\n          - *devbox\n", "      - *user\n      - *devbox\n    - age:\n", "      - *box\n      - *devbox\n# end\n"} {
		if !strings.Contains(got, after) {
			t.Errorf("no %q in:\n%s", after, got)
		}
	}
}

func TestAddRecipientAlreadyThere(t *testing.T) {
	in := strings.Replace(userSopsYAML, "creation_rules:", "  - &old "+pubB+"\ncreation_rules:", 1)
	p := writeSopsYAML(t, in)
	if anchor, changed, err := addRecipient(p, "devbox", pubB); err != nil || changed || anchor != "" {
		t.Fatalf("addRecipient = %q, %v, %v; want nothing done", anchor, changed, err)
	}
	if readString(t, p) != in {
		t.Error("the file changed")
	}
}

func TestAddRecipientTakenAnchor(t *testing.T) {
	in := strings.Replace(userSopsYAML, "&user", "&devbox", 1)
	in = strings.Replace(in, "*user", "*devbox", 1)
	p := writeSopsYAML(t, in)
	anchor, _, err := addRecipient(p, "devbox", pubB)
	if err != nil || anchor != "devbox-2" {
		t.Fatalf("anchor = %q, %v; want devbox-2", anchor, err)
	}
	if got := readString(t, p); !strings.Contains(got, "  - &devbox-2 "+pubB+"\n") || !strings.Contains(got, "          - *devbox\n          - *devbox-2\n") {
		t.Errorf("got:\n%s", got)
	}
}

func TestAddRecipientNoTrailingNewline(t *testing.T) {
	p := writeSopsYAML(t, strings.TrimSuffix(userSopsYAML, "\n"))
	if _, _, err := addRecipient(p, "devbox", pubB); err != nil {
		t.Fatal(err)
	}
	if got := readString(t, p); !strings.HasSuffix(got, "          - *user\n          - *devbox\n") {
		t.Errorf("got:\n%q", got)
	}
}

// Shapes addRecipient doesn't edit: refused, the file as it was.
func TestAddRecipientRefusedShapes(t *testing.T) {
	for name, in := range map[string]string{
		"no keys":          strings.SplitN(userSopsYAML, "creation_rules:", 2)[1],
		"flow keys":        "keys: [ " + pubA + " ]\ncreation_rules:\n  - key_groups:\n      - age:\n          - " + pubA + "\n",
		"age string":       "keys:\n  - &user " + pubA + "\ncreation_rules:\n  - path_regex: secrets/.*\n    age: " + pubA + "\n",
		"group age string": "keys:\n  - &user " + pubA + "\ncreation_rules:\n  - key_groups:\n      - age: " + pubA + "\n",
		"no rules":         "keys:\n  - &user " + pubA + "\n",
		"pgp only":         "keys:\n  - &user " + pubA + "\ncreation_rules:\n  - key_groups:\n      - pgp:\n          - 0123\n",
	} {
		p := writeSopsYAML(t, in)
		_, _, err := addRecipient(p, "devbox", pubB)
		var shape *recipientShapeError
		if !errors.As(err, &shape) {
			t.Errorf("%s: err = %v, want a recipientShapeError", name, err)
		}
		if readString(t, p) != in {
			t.Errorf("%s: the file changed", name)
		}
	}
	if l := recipientLines("devbox", pubB); !strings.Contains(l, "- &devbox "+pubB) || !strings.Contains(l, "- *devbox") {
		t.Errorf("recipientLines = %q", l)
	}
}

func TestSecretsReady(t *testing.T) {
	put := func(dir, rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oneGroup := "token: ENC[AES256_GCM,data:x]\nsops:\n    age:\n        - recipient: " + pubB + "\n          enc: |\n            x\n"
	keyGroups := "token: ENC[AES256_GCM,data:x]\nsops:\n    key_groups:\n        - age:\n            - recipient: " + pubB + "\n              enc: |\n                x\n"
	for name, body := range map[string]string{"one group": oneGroup, "key groups": keyGroups} {
		d := t.TempDir()
		put(d, "secrets/env/a.sops.yaml", body)
		if !secretsReady(d, pubB) {
			t.Errorf("%s: not ready for B", name)
		}
		if secretsReady(d, pubC) {
			t.Errorf("%s: ready for C", name)
		}
		if secretsReady(d, "") {
			t.Errorf("%s: ready with no key", name)
		}
	}
	// Every encrypted file must have it.
	d := t.TempDir()
	put(d, "secrets/env/a.sops.yaml", oneGroup)
	put(d, "secrets/env/b.sops.yaml", strings.ReplaceAll(oneGroup, pubB, pubA))
	if secretsReady(d, pubB) {
		t.Error("ready with one file not for B")
	}
	// A plaintext file (a team layer's skeleton, sops declined) has nothing
	// to decrypt; no files is ready too.
	d = t.TempDir()
	put(d, "secrets/env/api-keys.sops.yaml", "EXAMPLE_TOKEN: \"\"\n")
	if !secretsReady(d, pubB) || !secretsReady(t.TempDir(), pubB) {
		t.Error("plaintext or no files: not ready")
	}
}

// The real thing, with sops: a file encrypted for A, B's key added by
// addRecipient, re-encrypted by updatekeys with A's key, read with B's.
func TestSopsRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("sops"); err != nil {
		t.Skip("no sops on PATH")
	}
	t.Setenv("SOPS_AGE_KEY_FILE", "")
	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out}
	homeA, dir := t.TempDir(), t.TempDir()
	if err := a.ensureSops(homeA, dir, "boxa"); err != nil {
		t.Fatal(err)
	}
	keyB := filepath.Join(t.TempDir(), "keys.txt")
	pubB, err := ageKeygen(keyB)
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secrets", "env", "x.sops.yaml")
	if err := os.WriteFile(secret, []byte("token: example-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sops := func(key string, args ...string) (string, error) {
		c := exec.Command("sops", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+key)
		b, err := c.CombinedOutput()
		return string(b), err
	}
	if o, err := sops(ageKeyPath(homeA), "-e", "-i", "secrets/env/x.sops.yaml"); err != nil {
		t.Fatalf("sops -e: %v: %s", err, o)
	}
	if secretsReady(dir, pubB) {
		t.Fatal("ready for B before B was added")
	}
	if _, _, err := addRecipient(filepath.Join(dir, ".sops.yaml"), "boxb", pubB); err != nil {
		t.Fatal(err)
	}
	// A plaintext skeleton (a team layer's, sops declined, before piste
	// secrets edit): no recipients to change, and sops fails on it.
	skeleton := filepath.Join(dir, "secrets", "env", "skeleton.sops.yaml")
	if err := os.WriteFile(skeleton, []byte("EXAMPLE_TOKEN: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readString(t, secret)
	if err := a.updatekeys(dir, homeA); err != nil {
		t.Fatalf("updatekeys: %v\n%s", err, out.String())
	}
	if readString(t, secret) == before || !strings.Contains(out.String(), "Re-encrypted 1 file for .sops.yaml's recipients") {
		t.Fatalf("not re-encrypted:\n%s", out.String())
	}
	if readString(t, skeleton) != "EXAMPLE_TOKEN: \"\"\n" || !strings.Contains(out.String(), "Skipped secrets/env/skeleton.sops.yaml: not encrypted yet") {
		t.Fatalf("the plaintext file not skipped:\n%s", out.String())
	}
	if !secretsReady(dir, pubB) {
		t.Fatal("not ready for B after updatekeys")
	}
	if o, err := sops(keyB, "-d", "secrets/env/x.sops.yaml"); err != nil || !strings.Contains(o, "example-value") {
		t.Fatalf("sops -d with B: %v: %s", err, o)
	}
	// Every run rotates the data key (sops rotate): the value's ciphertext
	// is new, though the recipients are the same.
	if tokenCipher(t, readString(t, secret)) == tokenCipher(t, before) {
		t.Fatal("the data key wasn't rotated: the value's ciphertext is the same")
	}
	// B's machine is lost: its key out of .sops.yaml, updatekeys again. The
	// new data key never reached B, so what B kept of the old file is no
	// key to the new one.
	sopsYAML := filepath.Join(dir, ".sops.yaml")
	var kept []string
	for _, l := range strings.SplitAfter(readString(t, sopsYAML), "\n") {
		if !strings.Contains(l, "boxb") {
			kept = append(kept, l)
		}
	}
	if err := os.WriteFile(sopsYAML, []byte(strings.Join(kept, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	withB := readString(t, secret)
	out.Reset()
	if err := a.updatekeys(dir, homeA); err != nil || !strings.Contains(out.String(), "Re-encrypted 1 file") {
		t.Fatalf("after removing B: %v\n%s", err, out.String())
	}
	if tokenCipher(t, readString(t, secret)) == tokenCipher(t, withB) {
		t.Fatal("not rotated after removing B")
	}
	if secretsReady(dir, pubB) {
		t.Fatal("still ready for B after its key was removed")
	}
	if o, err := sops(keyB, "-d", "secrets/env/x.sops.yaml"); err == nil {
		t.Fatalf("B still decrypts: %s", o)
	}
	if o, err := sops(ageKeyPath(homeA), "-d", "secrets/env/x.sops.yaml"); err != nil || !strings.Contains(o, "example-value") {
		t.Fatalf("sops -d with A: %v: %s", err, o)
	}
	// Again with nothing changed in .sops.yaml: rotate still writes a new
	// data key, so the file changes, but the recipients didn't, and the
	// message says which.
	out.Reset()
	if err := a.updatekeys(dir, homeA); err != nil {
		t.Fatalf("no change: %v\n%s", err, out.String())
	}
	if o := out.String(); strings.Contains(o, "Re-encrypted") || !strings.Contains(o, "Recipients unchanged: every file already had .sops.yaml's. Each has a new data key now: commit and push the changes.") {
		t.Fatalf("no change in recipients:\n%s", o)
	}
}

// tokenCipher is the encrypted value of token: in a sops file.
func tokenCipher(t *testing.T, file string) string {
	t.Helper()
	for _, l := range strings.Split(file, "\n") {
		if v, ok := strings.CutPrefix(l, "token: "); ok {
			return v
		}
	}
	t.Fatalf("no token: in\n%s", file)
	return ""
}

// A *.sops.yaml with no sops: block but a value in it is a secret about to
// be committed in plaintext (piste init without sops writes the skeleton
// unencrypted and stages it): build, switch and secrets init refuse it.
// Empty values, comments only and encrypted files pass.
func TestPlaintextSecrets(t *testing.T) {
	put := func(dir, rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d := t.TempDir()
	put(d, "secrets/env/empty.sops.yaml", skeletonLater+"example:\n  token: \"\"\n  other: null\n  list: []\n")
	put(d, "secrets/env/comments.sops.yaml", "# nothing yet\n")
	put(d, "secrets/env/blank.sops.yaml", "")
	put(d, "secrets/env/enc.sops.yaml", "token: ENC[AES256_GCM,data:x]\nsops:\n    age:\n        - recipient: "+pubB+"\n")
	put(d, ".sops.yaml", userSopsYAML)
	if got := plaintextSecrets(d); len(got) != 0 {
		t.Fatalf("plaintextSecrets = %q, want none", got)
	}
	put(d, "secrets/env/typed.sops.yaml", "example:\n  token: not-a-real-value\n")
	put(d, "secrets/env/top.sops.yaml", "TOKEN: x\n")
	put(d, "secrets/env/number.sops.yaml", "PORT: 8080\n")
	put(d, "secrets/env/listed.sops.yaml", "keys:\n  - x\n")
	put(d, "secrets/env/broken.sops.yaml", "a: [\n")
	want := []string{
		"secrets/env/broken.sops.yaml",
		"secrets/env/listed.sops.yaml",
		"secrets/env/number.sops.yaml",
		"secrets/env/top.sops.yaml",
		"secrets/env/typed.sops.yaml",
	}
	if got := plaintextSecrets(d); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("plaintextSecrets = %q, want %q", got, want)
	}
	// Nothing under .git counts.
	g := t.TempDir()
	put(g, ".git/x.sops.yaml", "TOKEN: x\n")
	if got := plaintextSecrets(g); len(got) != 0 {
		t.Errorf("plaintextSecrets under .git = %q", got)
	}
}
