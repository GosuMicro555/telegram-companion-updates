package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakefileBuildsWindowsGeneratorAsWailsProductionApp(t *testing.T) {
	makefilePath := filepath.Join("..", "..", "Makefile")
	contents, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}

	source := string(contents)
	start := strings.Index(source, "build-license-generator-windows:")
	end := strings.Index(source[start:], "\ndesktop-build:")
	if start < 0 || end < 0 {
		t.Fatal("license generator build target is missing")
	}
	target := source[start : start+end]

	if !strings.Contains(target, "-tags production") {
		t.Fatal("license generator must be built with the Wails production tag")
	}
	if !strings.Contains(target, "./cmd/license-generator") {
		t.Fatal("license generator build target points to the wrong package")
	}
}
