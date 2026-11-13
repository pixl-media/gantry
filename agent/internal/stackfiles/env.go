package stackfiles

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidKey reports whether key is a valid variable name.
func ValidKey(key string) bool { return keyPattern.MatchString(key) }

// envFile is a parsed .env file that can be changed and written back with
// its comments, blank lines and order intact.
type envFile struct {
	entries []envEntry
}

// envEntry is one logical line: a variable, or anything else (comment,
// blank line, unparsable text) kept verbatim. A quoted value may span
// several raw lines.
type envEntry struct {
	raw   []string
	key   string // "" for non-variable lines
	value string // parsed value, for variables
}

func parseEnv(data []byte) *envFile {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	f := &envFile{}
	if text == "" {
		return f
	}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		key, rest, ok := splitAssignment(line)
		if !ok {
			f.entries = append(f.entries, envEntry{raw: []string{line}})
			continue
		}
		// A quoted value may continue on the next lines.
		raw := []string{line}
		value, closed := parseValue(rest)
		for !closed && i+1 < len(lines) {
			i++
			raw = append(raw, lines[i])
			value, closed = parseValue(strings.Join(append([]string{rest}, raw[1:]...), "\n"))
		}
		f.entries = append(f.entries, envEntry{raw: raw, key: key, value: value})
	}
	return f
}

// splitAssignment splits `[export ]KEY=value` into key and value text.
func splitAssignment(line string) (key, rest string, ok bool) {
	s := strings.TrimLeft(line, " \t")
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false
	}
	s = strings.TrimPrefix(s, "export ")
	k, v, found := strings.Cut(s, "=")
	if !found {
		return "", "", false
	}
	k = strings.TrimSpace(k)
	if !ValidKey(k) {
		return "", "", false
	}
	return k, v, true
}

// parseValue reads a value like Compose does: single quotes are literal,
// double quotes understand backslash escapes, unquoted values end at an
// inline " #" comment. closed is false if a quote isn't closed yet.
func parseValue(s string) (value string, closed bool) {
	s = strings.TrimLeft(s, " \t")
	switch {
	case strings.HasPrefix(s, "'"):
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return s[1:], false
		}
		return s[1 : 1+end], true
	case strings.HasPrefix(s, `"`):
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				i++
				switch n := s[i]; n {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				case '\\', '"', '$':
					b.WriteByte(n)
				default:
					b.WriteByte('\\')
					b.WriteByte(n)
				}
				continue
			}
			if c == '"' {
				return b.String(), true
			}
			b.WriteByte(c)
		}
		return b.String(), false
	default:
		for _, sep := range []string{" #", "\t#"} {
			if i := strings.Index(s, sep); i >= 0 {
				s = s[:i]
			}
		}
		return strings.TrimRight(s, " \t"), true
	}
}

// keys returns the variable names in file order, without duplicates.
func (f *envFile) keys() []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range f.entries {
		if e.key != "" && !seen[e.key] {
			seen[e.key] = true
			out = append(out, e.key)
		}
	}
	return out
}

// value returns a variable's value; with duplicates, the last one counts,
// as in Compose.
func (f *envFile) value(key string) (string, bool) {
	for i := len(f.entries) - 1; i >= 0; i-- {
		if f.entries[i].key == key {
			return f.entries[i].value, true
		}
	}
	return "", false
}

// set changes a variable in place (its first line; duplicates are removed)
// or appends it.
func (f *envFile) set(key, value string) error {
	if !ValidKey(key) {
		return &InvalidVariableError{Key: key, Reason: "not a valid name (letters, digits and _, not starting with a digit)"}
	}
	if strings.ContainsAny(value, "\n\r") {
		return &InvalidVariableError{Key: key, Reason: "values can't contain line breaks"}
	}
	line := key + "=" + quote(value)
	found := false
	kept := f.entries[:0]
	for _, e := range f.entries {
		if e.key == key {
			if found {
				continue // drop duplicates
			}
			found = true
			prefix := ""
			if strings.HasPrefix(strings.TrimLeft(e.raw[0], " \t"), "export ") {
				prefix = "export "
			}
			e = envEntry{raw: []string{prefix + line}, key: key, value: value}
		}
		kept = append(kept, e)
	}
	f.entries = kept
	if !found {
		f.entries = append(f.entries, envEntry{raw: []string{line}, key: key, value: value})
	}
	return nil
}

// remove deletes all lines of a variable.
func (f *envFile) remove(key string) {
	kept := f.entries[:0]
	for _, e := range f.entries {
		if e.key != key {
			kept = append(kept, e)
		}
	}
	f.entries = kept
}

func (f *envFile) bytes() []byte {
	var b bytes.Buffer
	for _, e := range f.entries {
		for _, r := range e.raw {
			b.WriteString(r)
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

var plainValue = regexp.MustCompile(`^[A-Za-z0-9_./:@%+,=-]+$`)

// quote writes a value so that Compose reads it back literally: plain
// values as they are, others in single quotes (no escapes, no variable
// expansion), and values containing a single quote in double quotes with
// \, " and $ escaped.
func quote(v string) string {
	switch {
	case plainValue.MatchString(v):
		return v
	case !strings.Contains(v, "'"):
		return "'" + v + "'"
	default:
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`)
		return `"` + r.Replace(v) + `"`
	}
}

// InvalidVariableError means a variable name or value can't be written.
type InvalidVariableError struct {
	Key    string
	Reason string
}

func (e *InvalidVariableError) Error() string {
	return fmt.Sprintf("variable %q: %s", e.Key, e.Reason)
}
