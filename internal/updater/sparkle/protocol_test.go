package sparkle

import "testing"

func TestNativeProtocolConstantsMatchObjectiveCBridge(t *testing.T) {
	states := []struct {
		name string
		got  int
		want int
	}{
		{name: "idle", got: nativeStateIdle, want: 0},
		{name: "pending", got: nativeStatePending, want: 1},
		{name: "done", got: nativeStateDone, want: 2},
		{name: "failed", got: nativeStateFailed, want: 3},
	}
	for _, state := range states {
		if state.got != state.want {
			t.Errorf("state %s = %d, want %d", state.name, state.got, state.want)
		}
	}

	operations := []struct {
		name string
		got  int
		want int
	}{
		{name: "check", got: nativeOperationCheck, want: 1},
		{name: "download", got: nativeOperationDownload, want: 2},
		{name: "install", got: nativeOperationInstall, want: 3},
	}
	for _, operation := range operations {
		if operation.got != operation.want {
			t.Errorf("operation %s = %d, want %d", operation.name, operation.got, operation.want)
		}
	}
}
