package stackfiles

import (
	"errors"
	"slices"
	"testing"
)

func TestParseValues(t *testing.T) {
	input := `# database
export DB_USER=admin
DB_PASSWORD='p@ss word #1'
GREETING="say \"hi\"\n$HOME"
PLAIN=value # a comment
  SPACED = spaced
EMPTY=
not a variable line
1BAD=x
MULTI="line one
line two"
DB_USER=override
`
	env := parseEnv([]byte(input))
	if got, want := env.keys(), []string{"DB_USER", "DB_PASSWORD", "GREETING", "PLAIN", "SPACED", "EMPTY", "MULTI"}; !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	tests := map[string]string{
		"DB_USER":     "override", // the last occurrence counts
		"DB_PASSWORD": "p@ss word #1",
		"GREETING":    "say \"hi\"\n$HOME",
		"PLAIN":       "value",
		"SPACED":      "spaced",
		"EMPTY":       "",
		"MULTI":       "line one\nline two",
	}
	for k, want := range tests {
		if got, ok := env.value(k); !ok || got != want {
			t.Errorf("%s = %q, %v; want %q", k, got, ok, want)
		}
	}
	// Unchanged files are written back byte for byte.
	if got := string(env.bytes()); got != input {
		t.Errorf("round trip changed the file:\n%s", got)
	}
}

func TestSetKeepsLayout(t *testing.T) {
	env := parseEnv([]byte("# header\nexport A=1\n\nB=2 # keep me\nA=dup\n"))
	if err := env.set("A", "new value"); err != nil {
		t.Fatal(err)
	}
	if err := env.set("C", "3"); err != nil {
		t.Fatal(err)
	}
	env.remove("B")
	want := "# header\nexport A='new value'\n\nC=3\n"
	if got := string(env.bytes()); got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
}

func TestQuoteRoundTrip(t *testing.T) {
	values := []string{
		"plain", "with space", "it's", `say "hi"`, "$HOME and ${X}",
		"it's $5 \\ \"quoted\"", "#hash", "trailing ", " leading", "ümlaut ✓", "",
	}
	for _, v := range values {
		env := parseEnv(nil)
		if err := env.set("K", v); err != nil {
			t.Fatal(err)
		}
		back, _ := parseEnv(env.bytes()).value("K")
		if back != v {
			t.Errorf("value %q came back as %q (written as %q)", v, back, env.bytes())
		}
	}
}

func TestSetRejects(t *testing.T) {
	env := parseEnv(nil)
	var invalid *InvalidVariableError
	for _, tt := range []struct{ key, value string }{
		{"1ABC", "x"}, {"WITH-DASH", "x"}, {"", "x"}, {"OK", "line\nbreak"},
	} {
		if err := env.set(tt.key, tt.value); !errors.As(err, &invalid) {
			t.Errorf("set(%q, %q) = %v, want InvalidVariableError", tt.key, tt.value, err)
		}
	}
}
