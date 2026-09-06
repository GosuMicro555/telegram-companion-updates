package app

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFirstLaunchLinuxProductionRenameFailsClosed(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate rename contract test")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "first_launch_rename_linux.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{
		[]byte("AT_FDCWD"),
		[]byte("Renameat2"),
		[]byte("golang.org/x/sys/unix"),
	} {
		if bytes.Contains(source, forbidden) {
			t.Fatalf("Linux production rename contains unsafe primitive %q", forbidden)
		}
	}
	for _, function := range []string{
		"firstLaunchRenameNoReplace",
		"firstLaunchExchangePaths",
	} {
		want := []byte("func " + function + "(string, string) error {\n\treturn errFirstLaunchAtomicRenameUnsupported\n}")
		if !bytes.Contains(source, want) {
			t.Fatalf("%s does not fail closed with errFirstLaunchAtomicRenameUnsupported", function)
		}
	}
}
