package wails

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheduledDMBindingSurfaceExposesOnlyWorkflowMethods(t *testing.T) {
	want := map[string]struct{}{
		"ListScheduledDMTasks":         {},
		"SaveScheduledDMTask":          {},
		"StartScheduledDMTask":         {},
		"StopScheduledDMTask":          {},
		"CancelScheduledDMTask":        {},
		"ResolveScheduledDMRecipients": {},
	}

	typ := reflect.TypeOf((*Bindings)(nil))
	var got []string
	for index := 0; index < typ.NumMethod(); index++ {
		name := typ.Method(index).Name
		if strings.Contains(name, "ScheduledDM") {
			got = append(got, name)
		}
	}
	require.ElementsMatch(t, []string{
		"ListScheduledDMTasks",
		"SaveScheduledDMTask",
		"StartScheduledDMTask",
		"StopScheduledDMTask",
		"CancelScheduledDMTask",
		"ResolveScheduledDMRecipients",
	}, got)
	require.Len(t, got, len(want))
}

func TestScheduledDMGeneratedSurfaceDoesNotContainRuntimeConfiguration(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Join(filepath.Dir(filename), "..", "..", "..")
	for _, relative := range []string{
		filepath.Join("frontend", "wailsjs", "go", "wails", "Bindings.js"),
		filepath.Join("frontend", "wailsjs", "go", "wails", "Bindings.d.ts"),
		filepath.Join("frontend", "wailsjs", "go", "models.ts"),
	} {
		contents, err := os.ReadFile(filepath.Join(root, relative))
		require.NoError(t, err, relative)
		require.NotContains(t, string(contents), "ConfigureScheduledDM", relative)
		require.NotContains(t, string(contents), "ScheduledDMRuntime", relative)
	}
}
