//go:build desktop && darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"telegram-companion/internal/buildinfo"
	"telegram-companion/internal/license"
	wailsbindings "telegram-companion/internal/transport/wails"
)

func TestRelaunchAwaitWithoutMarkerReturnsNormally(t *testing.T) {
	t.Setenv(desktopRelaunchMarkerEnvironment, "")
	require.NoError(t, os.Unsetenv(desktopRelaunchMarkerEnvironment))

	require.NoError(t, awaitDesktopRelaunchHandoff())
}

func TestRelaunchAwaitRejectsMalformedMarkerAndStripsIt(t *testing.T) {
	t.Setenv(desktopRelaunchMarkerEnvironment, "not-a-private-handoff-marker")

	err := awaitDesktopRelaunchHandoff()

	require.Error(t, err)
	_, present := os.LookupEnv(desktopRelaunchMarkerEnvironment)
	require.False(t, present)
}

func TestRelaunchAwaitRejectsSpoofedDescriptor(t *testing.T) {
	token := bytes.Repeat([]byte{0x41}, desktopRelaunchTokenBytes)
	regular, err := os.CreateTemp(t.TempDir(), "spoofed-handoff-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = regular.Close() })

	err = awaitDesktopRelaunchHandoffFrom(desktopRelaunchMarker(token), regular)

	require.Error(t, err)
}

func TestRelaunchRawDescriptorValidationRejectsRegularFile(t *testing.T) {
	regular, err := os.CreateTemp(t.TempDir(), "spoofed-fd3-")
	require.NoError(t, err)
	defer regular.Close()

	require.Error(t, validateDesktopRelaunchDescriptor(regular.Fd()))
}

func TestRelaunchAwaitRejectsMismatchedPrivateMarker(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	t.Cleanup(func() { _ = writer.Close() })
	require.NoError(t, writeAll(writer, bytes.Repeat([]byte{0x42}, desktopRelaunchTokenBytes)))
	require.NoError(t, writer.Close())

	err = awaitDesktopRelaunchHandoffFrom(
		desktopRelaunchMarker(bytes.Repeat([]byte{0x41}, desktopRelaunchTokenBytes)),
		reader,
	)

	require.Error(t, err)
}

func TestRelaunchAwaitBlocksUntilPredecessorReleasesWriter(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() })
	token := bytes.Repeat([]byte{0x41}, desktopRelaunchTokenBytes)
	require.NoError(t, writeAll(writer, token))

	done := make(chan error, 1)
	go func() {
		done <- awaitDesktopRelaunchHandoffFrom(desktopRelaunchMarker(token), reader)
	}()
	require.NoError(t, writeAll(writer, []byte{0x01}))
	select {
	case err := <-done:
		t.Fatalf("successor returned before predecessor release: %v", err)
	default:
	}

	relauncher := &desktopRelaunchCoordinator{writer: writer, prepared: true}
	require.NoError(t, relauncher.Release())
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("successor did not return after predecessor release")
	}
}

func TestRelaunchPrepareUsesSameExecutablePrivateMarkerAndFD3(t *testing.T) {
	executable := "/Applications/Telegram Companion.app/Contents/MacOS/telegram-companion"
	var capturedName string
	var capturedArgs []string
	var capturedAttr *os.ProcAttr
	fd3WasPipe := false
	relauncher := &desktopRelaunchCoordinator{
		executable: func() (string, error) { return executable, nil },
		arguments:  func() []string { return []string{executable, "--flag"} },
		environment: func() []string {
			return []string{"VISIBLE=value", desktopRelaunchMarkerEnvironment + "=stale"}
		},
		random: bytes.NewReader(bytes.Repeat([]byte{0x41}, desktopRelaunchTokenBytes)),
		startProcess: func(name string, args []string, attr *os.ProcAttr) (*os.Process, error) {
			capturedName = name
			capturedArgs = append([]string(nil), args...)
			capturedAttr = attr
			if len(attr.Files) == 4 && attr.Files[3] != nil {
				info, statErr := attr.Files[3].Stat()
				fd3WasPipe = statErr == nil && info.Mode()&os.ModeNamedPipe != 0
			}
			return &os.Process{}, nil
		},
		releaseProcess: func(*os.Process) error { return nil },
	}

	require.NoError(t, relauncher.Request(context.Background()))
	t.Cleanup(func() { _ = relauncher.Release() })

	require.Equal(t, executable, capturedName)
	require.Equal(t, []string{executable, "--flag"}, capturedArgs)
	require.NotNil(t, capturedAttr)
	require.Equal(t, filepath.Dir(executable), capturedAttr.Dir)
	require.Equal(t, []*os.File{os.Stdin, os.Stdout, os.Stderr}, capturedAttr.Files[:3])
	require.True(t, fd3WasPipe)
	require.Equal(t, "VISIBLE=value", capturedAttr.Env[0])
	markers := environmentValues(capturedAttr.Env, desktopRelaunchMarkerEnvironment)
	require.Len(t, markers, 1)
	require.Equal(t, desktopRelaunchMarker(bytes.Repeat([]byte{0x41}, desktopRelaunchTokenBytes)), markers[0])
}

func TestRelaunchStartFailureClosesPipeDoesNotCommitAndCanRetry(t *testing.T) {
	var failedReader *os.File
	var failedWriter *os.File
	starts := 0
	relauncher := &desktopRelaunchCoordinator{
		executable:  func() (string, error) { return "/tmp/telegram-companion", nil },
		arguments:   func() []string { return []string{"/tmp/telegram-companion"} },
		environment: func() []string { return nil },
		random:      bytes.NewReader(bytes.Repeat([]byte{0x41}, desktopRelaunchTokenBytes*2)),
		pipe: func() (*os.File, *os.File, error) {
			reader, writer, err := os.Pipe()
			if starts == 0 {
				failedReader, failedWriter = reader, writer
			}
			return reader, writer, err
		},
		startProcess: func(string, []string, *os.ProcAttr) (*os.Process, error) {
			starts++
			if starts == 1 {
				return nil, errors.New("permission denied")
			}
			return &os.Process{}, nil
		},
		releaseProcess: func(*os.Process) error { return nil },
	}

	first := relauncher.Request(context.Background())
	require.Error(t, first)
	require.Error(t, failedReader.Close())
	require.Error(t, failedWriter.Close())
	require.NoError(t, relauncher.Request(context.Background()))
	require.Equal(t, 2, starts)
	require.NoError(t, relauncher.Release())
}

func TestRelaunchProcessReleaseFailureStillCommitsSingleHandoff(t *testing.T) {
	starts := 0
	releases := 0
	relauncher := &desktopRelaunchCoordinator{
		executable:  func() (string, error) { return "/tmp/telegram-companion", nil },
		arguments:   func() []string { return []string{"/tmp/telegram-companion"} },
		environment: func() []string { return nil },
		random:      bytes.NewReader(bytes.Repeat([]byte{0x41}, desktopRelaunchTokenBytes)),
		startProcess: func(string, []string, *os.ProcAttr) (*os.Process, error) {
			starts++
			return &os.Process{}, nil
		},
		releaseProcess: func(*os.Process) error {
			releases++
			return errors.New("release failed")
		},
	}

	gate := &relaunchActivationGate{snapshot: license.Snapshot{
		Mode: buildinfo.ChannelPublicMacOSARM64, State: license.StateNeedsActivation, MachineID: "ABC123",
	}}
	quitCalls := 0
	binding := wailsbindings.NewActivationBindingsWithRestartRequester(
		gate,
		nil,
		relauncher,
		func(context.Context) { quitCalls++ },
		func(callback func()) { callback() },
	)

	first := binding.ActivateLicenseKey("TCPLIC1.payload.signature")
	second := binding.ActivateLicenseKey("TCPLIC1.payload.signature")

	require.Equal(t, "activated", first.State)
	require.Equal(t, "activated", second.State)
	require.Equal(t, 1, starts)
	require.Equal(t, 1, releases)
	require.Equal(t, 1, quitCalls)
	require.NoError(t, relauncher.Release())
}

func TestRelaunchConcurrentRequestsSpawnOneSuccessor(t *testing.T) {
	var startsMu sync.Mutex
	starts := 0
	relauncher := &desktopRelaunchCoordinator{
		executable:  func() (string, error) { return "/tmp/telegram-companion", nil },
		arguments:   func() []string { return []string{"/tmp/telegram-companion"} },
		environment: func() []string { return nil },
		random:      bytes.NewReader(bytes.Repeat([]byte{0x41}, desktopRelaunchTokenBytes)),
		startProcess: func(string, []string, *os.ProcAttr) (*os.Process, error) {
			startsMu.Lock()
			starts++
			startsMu.Unlock()
			return &os.Process{}, nil
		},
		releaseProcess: func(*os.Process) error { return nil },
	}

	const requests = 16
	results := make(chan error, requests)
	for range requests {
		go func() { results <- relauncher.Request(context.Background()) }()
	}
	for range requests {
		require.NoError(t, <-results)
	}
	require.NoError(t, relauncher.Release())
	startsMu.Lock()
	defer startsMu.Unlock()
	require.Equal(t, 1, starts)
}

func TestRelaunchAwaitIsFirstStatementOfMain(t *testing.T) {
	mainFunction := parsedMainFunction(t)
	require.NotEmpty(t, mainFunction.Body.List)
	first, ok := mainFunction.Body.List[0].(*ast.IfStmt)
	require.True(t, ok, "main must begin with the relaunch await error guard")
	assignment, ok := first.Init.(*ast.AssignStmt)
	require.True(t, ok)
	require.Len(t, assignment.Rhs, 1)
	require.Equal(t, "awaitDesktopRelaunchHandoff", calledIdentifier(assignment.Rhs[0]))
}

func TestRelaunchWriterReleaseImmediatelyFollowsRunnerReturn(t *testing.T) {
	mainFunction := parsedMainFunction(t)
	for index, statement := range mainFunction.Body.List {
		assignment, ok := statement.(*ast.AssignStmt)
		if !ok || len(assignment.Rhs) != 1 || calledIdentifier(assignment.Rhs[0]) != "runDesktopProgram" {
			continue
		}
		require.Greater(t, len(mainFunction.Body.List), index+1)
		expression, ok := mainFunction.Body.List[index+1].(*ast.ExprStmt)
		require.True(t, ok, "handoff writer release must immediately follow runDesktopProgram return")
		call, ok := expression.X.(*ast.CallExpr)
		require.True(t, ok)
		selector, ok := call.Fun.(*ast.SelectorExpr)
		require.True(t, ok)
		require.Equal(t, "Release", selector.Sel.Name)
		return
	}
	t.Fatal("main does not assign the runDesktopProgram result")
}

func parsedMainFunction(t *testing.T) *ast.FuncDecl {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	require.NoError(t, err)
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "main" {
			return function
		}
	}
	t.Fatal("main function not found")
	return nil
}

func calledIdentifier(expression ast.Expr) string {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return ""
	}
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		data = data[written:]
	}
	return nil
}

func environmentValues(environment []string, key string) []string {
	prefix := key + "="
	values := make([]string, 0, 1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			values = append(values, strings.TrimPrefix(entry, prefix))
		}
	}
	return values
}

type relaunchActivationGate struct {
	snapshot license.Snapshot
}

func (g *relaunchActivationGate) Snapshot() license.Snapshot { return g.snapshot }

func (g *relaunchActivationGate) Activate(string) license.Snapshot {
	g.snapshot.State = license.StateActivated
	g.snapshot.ErrorCode = ""
	return g.snapshot
}
