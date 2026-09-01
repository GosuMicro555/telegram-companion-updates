package wails

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"telegram-companion/internal/domain"
	"telegram-companion/internal/usecase"
)

var errUnknownBindingAction = errors.New("unknown binding action")

func dispatchBindingForContract(receiver any, action string, arguments ...any) ([]any, error) {
	method := reflect.ValueOf(receiver).MethodByName(action)
	if !method.IsValid() || method.Type().NumIn() != len(arguments) {
		return nil, errUnknownBindingAction
	}
	inputs := make([]reflect.Value, len(arguments))
	for index, argument := range arguments {
		if argument == nil {
			return nil, errUnknownBindingAction
		}
		value := reflect.ValueOf(argument)
		if !value.Type().AssignableTo(method.Type().In(index)) {
			return nil, errUnknownBindingAction
		}
		inputs[index] = value
	}
	reflected := method.Call(inputs)
	if len(reflected) > 0 && reflected[len(reflected)-1].Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		last := reflected[len(reflected)-1]
		reflected = reflected[:len(reflected)-1]
		if !last.IsNil() {
			return nil, last.Interface().(error)
		}
	}
	result := make([]any, len(reflected))
	for index := range reflected {
		result[index] = reflected[index].Interface()
	}
	return result, nil
}

func TestBindingsHaveNoLocalImportActions(t *testing.T) {
	bindingsType := reflect.TypeOf(&Bindings{})
	for _, method := range []string{
		"SubmitTData" + "DriveLinks",
		"ListTData" + "ImportItems",
	} {
		if _, exists := bindingsType.MethodByName(method); exists {
			t.Errorf("Bindings still expose removed local import action %s", method)
		}
	}
}

func TestStaleLocalImportActionsAreUnknownAndPreserveExistingAccount(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "sessions", "account.session")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(session fixture) error = %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("synthetic-session-state"), 0o600); err != nil {
		t.Fatalf("WriteFile(session fixture) error = %v", err)
	}
	store := &settingsStoreStub{
		settings:    domain.DefaultKeywordSettings(),
		appSettings: domain.DefaultAppSettings(),
		accounts: []domain.Account{{
			ID:          "account-existing",
			DisplayName: "Existing local account",
			Role:        domain.AccountRoleSpammer,
			SessionPath: sessionPath,
			Status:      domain.AccountActive,
		}},
	}
	bindings := NewBindings(usecase.NewAutomationController(nil), store)
	if _, err := dispatchBindingForContract(bindings, "GetAccounts"); err != nil {
		t.Fatalf("positive-control dispatch error = %v", err)
	}

	beforeAccounts, err := store.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts(before) error = %v", err)
	}
	beforeSession, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("ReadFile(session before) error = %v", err)
	}
	info, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatalf("Stat(session fixture) error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("session fixture mode = %o, want 600", info.Mode().Perm())
	}

	for _, action := range []string{
		"SubmitTData" + "DriveLinks",
		"ListTData" + "ImportItems",
	} {
		result, staleErr := dispatchBindingForContract(bindings, action, "synthetic-input")
		if staleErr == nil || !errors.Is(staleErr, errUnknownBindingAction) || staleErr.Error() != "unknown binding action" {
			t.Fatalf("stale action %s error = %v, want generic unknown binding action", action, staleErr)
		}
		if result != nil {
			t.Fatalf("stale action %s result = %#v, want nil", action, result)
		}
	}

	afterAccounts, err := store.ListAccounts(context.Background())
	if err != nil {
		t.Fatalf("ListAccounts(after) error = %v", err)
	}
	afterSession, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("ReadFile(session after) error = %v", err)
	}
	if !reflect.DeepEqual(afterAccounts, beforeAccounts) {
		t.Fatalf("stale action mutated account state: before=%#v after=%#v", beforeAccounts, afterAccounts)
	}
	if !bytes.Equal(afterSession, beforeSession) {
		t.Fatal("stale action mutated the existing session fixture")
	}
}
