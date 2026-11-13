package userscope

import "testing"

func TestEncodings(t *testing.T) {
	v, err := ParseOrdered([]byte(`{"b": 1, "a": [1.0, 2.50, 1e-05, -0, 3], "c": {"z": "é😀\u0001<>&/\u2028", "y": null, "x": true}, "e": {}, "f": []}`))
	if err != nil {
		t.Fatal(err)
	}
	// Key order as read, numbers as written, UTF-8 as is, no HTML escaping.
	if got, want := encode(v, ""), `{"b":1,"a":[1.0,2.50,1e-05,-0,3],"c":{"z":"é😀\u0001<>&/\u2028","y":null,"x":true},"e":{},"f":[]}`; got != want {
		t.Errorf("encode:\n got %s\nwant %s", got, want)
	}
	if got, want := canonical(v), `{"a":[1.0,2.50,1e-05,-0,3],"b":1,"c":{"x":true,"y":null,"z":"é😀\u0001<>&/\u2028"},"e":{},"f":[]}`; got != want {
		t.Errorf("canonical:\n got %s\nwant %s", got, want)
	}
	// The string rules canonical's doc spells out.
	esc, err := ParseOrdered([]byte(`["\b\t\n\f\r\u0000\u001f\u007f\"\\\/\u2028\u2029", 1e2, 1E2, 100]`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := canonical(esc), `["\b\t\n\f\r\u0000\u001f`+"\x7f"+`\"\\/\u2028\u2029",1e2,1E2,100]`; got != want {
		t.Errorf("canonical strings and numbers:\n got %s\nwant %s", got, want)
	}
	o, _ := ParseOrdered([]byte(`{"z": [1, {}], "a": "x"}`))
	if got, want := encode(o, "  "), "{\n  \"z\": [\n    1,\n    {}\n  ],\n  \"a\": \"x\"\n}"; got != want {
		t.Errorf("indented:\n%s", got)
	}
	for in, want := range map[any]string{"s": "s", nil: "null", true: "true", 3: "3"} {
		if got := str(in); got != want {
			t.Errorf("str(%v) = %s", in, got)
		}
	}
}

// The version-1 layout, for migrating old ledgers: these bytes and ids are
// what piste wrote under ledger version 1.
func TestLegacyCanonical(t *testing.T) {
	for _, c := range []struct{ in, want, id string }{
		{`{"matcher":"Bash","hooks":[{"type":"command","command":"\"$HOME/x\" — café 😀","timeout":45}]}`,
			`{"hooks":[{"command":"\"$HOME/x\" \u2014 caf\u00e9 \ud83d\ude00","timeout":45,"type":"command"}],"matcher":"Bash"}`,
			"f608c0e3c8633eafc6233b2dd75999f4c0ff3ca8c248b084f6216dc1105c07da"},
		{`{"matcher":"","hooks":[{"timeout":1.50,"n":-0,"e":1E2,"f":1e-7,"g":12345678901234567890,"s":"<a&b> \u0001` + "\x7f" + `\t\b\f\\/"}]}`,
			`{"hooks":[{"e":100.0,"f":1e-07,"g":12345678901234567890,"n":0,"s":"<a&b>\u2028\u0001` + "\x7f" + `\t\b\f\\/","timeout":1.5}],"matcher":""}`,
			"e8ec46b401259937b8bbf26c8aacb8d49de24e3ab3684183ba71c6d0e1150d97"},
		{`{"z":1,"a":{"y":[],"b":{}},"é":null,"B":true}`,
			`{"B":true,"a":{"b":{},"y":[]},"z":1,"\u00e9":null}`,
			"5a3cd850eba4dd3c4e6859782e65d7ed510305fd971dc146bfb6f885fc1ba7d9"},
	} {
		v, err := ParseOrdered([]byte(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if got := legacyCanonical(v); got != c.want {
			t.Errorf("legacyCanonical:\n got %s\nwant %s", got, c.want)
		}
		if got := legacyGroupID("PreToolUse", v); got != c.id {
			t.Errorf("legacyGroupID(%s) = %s, want %s", c.in, got, c.id)
		}
	}
}
