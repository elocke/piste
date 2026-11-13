package machine

import (
	"strings"
	"testing"
)

func TestScanHidesCommentsAndStringText(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		visible []string // still in the code view
		hidden  []string // blanked
	}{
		{"line comment", "a # <x>\n<b>", []string{"<b>"}, []string{"<x>"}},
		{"block comment", "/* <x>\n<y> */ <z>", []string{"<z>"}, []string{"<x>", "<y>"}},
		{"unterminated block comment", "<a> /* <x>", []string{"<a>"}, []string{"<x>"}},
		{"string", `"<x>" <y>`, []string{"<y>"}, []string{"<x>"}},
		{"escaped quote", `"a\"<x>" <y>`, []string{"<y>"}, []string{"<x>"}},
		{"escaped interpolation", `"\${<x>}" <y>`, []string{"<y>"}, []string{"<x>"}},
		{"interpolation is code", `"a ${ <x> } b" <y>`, []string{"<x>", "<y>"}, nil},
		{"string inside interpolation", `"${ "<in>" + <out> }" <y>`, []string{"<out>", "<y>"}, []string{"<in>"}},
		{"braces inside interpolation", `"${ { a = 1; }.a }<x>" <y>`, []string{"<y>"}, []string{"<x>"}},
		{"indented string", "'' <x>\n <y> '' <z>", []string{"<z>"}, []string{"<x>", "<y>"}},
		{"indented escapes", "''a '''<x> ''$<y> ''\\n<w> '' <z>", []string{"<z>"}, []string{"<x>", "<y>", "<w>"}},
		{"indented interpolation", "'' ${ <x> } <y> '' <z>", []string{"<x>", "<z>"}, []string{"<y>"}},
		{"indented ${ escape", "'' ''${<x>} '' <y>", []string{"<y>"}, []string{"<x>"}},
		{"quotes in an identifier", "x'' <y> y'", []string{"<y>"}, nil},
		{"$$ is text in an indented string", "'' a $${b <x> '' <y>", []string{"<y>"}, []string{"<x>"}},
		{"$$ is text in a string", `"a $${b <x>" <y>`, []string{"<y>"}, []string{"<x>"}},
		{"$$ then an interpolation", `"$$${ <x> }" <y>`, []string{"<x>", "<y>"}, nil},
		{"# in a string", `"a # b" <y>`, []string{"<y>"}, nil},
		{"string in a comment", `# " <x>` + "\n<y>", []string{"<y>"}, []string{"<x>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := string(scan([]byte(tt.src)).code)
			if len(code) != len(tt.src) {
				t.Fatalf("code is %d bytes, src %d", len(code), len(tt.src))
			}
			for _, v := range tt.visible {
				if !strings.Contains(code, v) {
					t.Errorf("%q hidden in %q", v, code)
				}
			}
			for _, h := range tt.hidden {
				if strings.Contains(code, h) {
					t.Errorf("%q visible in %q", h, code)
				}
			}
			if strings.Count(code, "\n") != strings.Count(tt.src, "\n") {
				t.Errorf("newlines moved: %q", code)
			}
		})
	}
}

func TestScanUnsafeLines(t *testing.T) {
	src := "a\n''\n${ <x> }\n''\n/* c\nd */\ne\n\"x\ny\"\nz\n"
	sc := scan([]byte(src))
	for line, wantUnsafe := range map[int]bool{
		1:  false, // a
		2:  false, // '' (starts in code)
		3:  true,  // ${ <x> }
		4:  true,  // ''
		5:  false, // /* c
		6:  true,  // d */
		7:  false, // e
		8:  false, // "x
		9:  true,  // y"
		10: false,
	} {
		off := 0
		for i := 1; i < line; i++ {
			off += strings.Index(src[off:], "\n") + 1
		}
		if got := sc.unsafe[off]; got != wantUnsafe {
			t.Errorf("line %d unsafe = %v, want %v", line, got, wantUnsafe)
		}
	}
}

func TestTopLevel(t *testing.T) {
	tests := []struct {
		src     string
		shape   topShape
		formals string
	}{
		{"{ a, b }: x", formalsShape, "{ a, b }"},
		{"{ a ? { }, ... }: x", formalsShape, "{ a ? { }, ... }"},
		{"{ a ? \"}\" }: x", formalsShape, `{ a ? "}" }`},
		{"{ a }@args: x", formalsShape, "{ a }"},
		{"{ a } @ args : x", formalsShape, "{ a }"},
		{"args@{ a }: x", formalsShape, "{ a }"},
		{"args @ { a, ... } : x", formalsShape, "{ a, ... }"},
		{"# c\n/* d */\n\n{ a }: x", formalsShape, "{ a }"},
		{"{ }: x", formalsShape, "{ }"},
		{"{ a = 1; }", attrsShape, ""},
		{"{ a = { b = 1; }; } // { c = 2; }", attrsShape, ""},
		{"pkgs: { }", otherShape, ""},
		{"let a = 1; in { }", attrsShape, ""},
		{"let a = let b = 1; in b; in { }", attrsShape, ""},
		{"let p = ./let/in/x.nix; in { }", attrsShape, ""},
		{"let a = 1; in with b; rec { }", attrsShape, ""},
		{"# c\nlet a = 1; in { }", attrsShape, ""},
		{"with x; { }", attrsShape, ""},
		{"with x; with y; { }", attrsShape, ""},
		{"with import <nixpkgs> { }; { }", attrsShape, ""},
		{"with (let a = 1; in a); { }", attrsShape, ""},
		{"with { a = 1; }; { b = a; }", attrsShape, ""},
		{"rec { a = 1; }", attrsShape, ""},
		{"let a = 1; in b: { }", otherShape, ""},
		{"let a = 1; in { a }: x", otherShape, ""},
		{"let a = 1; in x@{ a }: x", otherShape, ""},
		{"let a = 1; in [ ]", otherShape, ""},
		{"let a = 1; { }", otherShape, ""},
		{"let { body = 1; }", otherShape, ""},
		{"with x; [ ]", otherShape, ""},
		{"with x { }", otherShape, ""},
		{"rec", otherShape, ""},
		{"let a = 1; in", otherShape, ""},
		{"let a = 1; in\n  # nothing after it\n", otherShape, ""},
		{"with x;", otherShape, ""},
		{"with x;\n", otherShape, ""},
		{"let a = 1; in with b;", otherShape, ""},
		{"let", otherShape, ""},
		{"with", otherShape, ""},
		{"", otherShape, ""},
		{"import ./a.nix", otherShape, ""},
		{"{ a }@args x", otherShape, ""},
		{"args@x: x", otherShape, ""},
		{"( { a }: x )", otherShape, ""},
	}
	for _, tt := range tests {
		code := scan([]byte(tt.src)).code
		got := topLevel(code)
		if want := skipSpace(code, 0); got.first != want {
			t.Errorf("%q: first = %d, want %d", tt.src, got.first, want)
		}
		if got.shape != tt.shape {
			t.Errorf("%q: shape = %v, want %v", tt.src, got.shape, tt.shape)
			continue
		}
		if tt.shape == formalsShape {
			if f := tt.src[got.open : got.close+1]; f != tt.formals {
				t.Errorf("%q: formals = %q, want %q", tt.src, f, tt.formals)
			}
		}
	}
}

func TestFormalEdit(t *testing.T) {
	tests := []struct{ src, want string }{
		{"{ a }: x", "{ modulesPath, a }: x"},
		{"{a}: x", "{modulesPath, a}: x"},
		{"{ ... }: x", "{ modulesPath, ... }: x"},
		{"{ }: x", "{ modulesPath }: x"},
		{"{}: x", "{modulesPath }: x"},
		{"{ # why\n  a,\n}: x", "{ modulesPath, # why\n  a,\n}: x"},
		{"{\n  a,\n  b,\n}: x", "{\n  modulesPath,\n  a,\n  b,\n}: x"},
		{"{\n\n\t# c\n\ta,\n}: x", "{\n\tmodulesPath,\n\n\t# c\n\ta,\n}: x"},
		{"args@{\n    a,\n}: x", "args@{\n    modulesPath,\n    a,\n}: x"},
	}
	for _, tt := range tests {
		code := scan([]byte(tt.src)).code
		top := topLevel(code)
		if top.shape != formalsShape {
			t.Errorf("%q: not formals", tt.src)
			continue
		}
		e := formalEdit([]byte(tt.src), top.open)
		if got := tt.src[:e.start] + e.text + tt.src[e.end:]; got != tt.want {
			t.Errorf("%q:\n got %q\nwant %q", tt.src, got, tt.want)
		}
	}
}
