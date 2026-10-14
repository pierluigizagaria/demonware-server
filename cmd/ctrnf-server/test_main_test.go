package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integration fixtures that exercise the legacy JSON fallback must never write
// synthetic accounts into the source directory after the package moves.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ctrnf-fixtures-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture directory:", err)
		os.Exit(1)
	}
	tempRoot, err := filepath.Abs(os.TempDir())
	if err != nil || filepath.Dir(dir) != tempRoot || !strings.HasPrefix(filepath.Base(dir), "ctrnf-fixtures-") {
		fmt.Fprintln(os.Stderr, "unexpected fixture directory")
		os.Exit(1)
	}
	testCTR.Economy.Dir = dir
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintln(os.Stderr, "fixture cleanup:", err)
		code = 1
	}
	os.Exit(code)
}
