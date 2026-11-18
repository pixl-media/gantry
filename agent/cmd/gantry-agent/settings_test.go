package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every variable the agent reads has a row in the README's configuration
// table and a line in the package's settings template, which becomes
// /etc/gantry/gantry.env; the template names nothing the agent doesn't read.
// Otherwise the three drift apart with the next setting.
func TestSettingsAreDocumented(t *testing.T) {
	src := readFile(t, "main.go")
	readme := readFile(t, "../../../README.md")
	template := readFile(t, "../../../packaging/gantry.env.example")

	read := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?:os\.Getenv|envOr|loadSecret)\("([A-Z_]+)"`).FindAllStringSubmatch(src, -1) {
		read[m[1]] = true
	}
	// loadSecret also reads NAME_FILE.
	for _, m := range regexp.MustCompile(`loadSecret\("([A-Z_]+)"`).FindAllStringSubmatch(src, -1) {
		read[m[1]+"_FILE"] = true
	}
	if len(read) < 15 {
		t.Fatalf("found only %d variables in main.go; does the pattern still match how they are read?", len(read))
	}

	for name := range read {
		if !strings.Contains(readme, "| `"+name+"` |") {
			t.Errorf("%s has no row in the README's configuration table", name)
		}
		if !regexp.MustCompile(`(?m)^#?` + name + `=`).MatchString(template) {
			t.Errorf("%s is missing from packaging/gantry.env.example", name)
		}
	}
	for _, m := range regexp.MustCompile(`(?m)^#?([A-Z_]+)=`).FindAllStringSubmatch(template, -1) {
		if !read[m[1]] {
			t.Errorf("packaging/gantry.env.example names %s, which the agent doesn't read", m[1])
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
