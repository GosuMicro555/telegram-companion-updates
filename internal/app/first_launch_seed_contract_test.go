package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

var firstLaunchRequiredSecretNamesForTest = []string{
	"outbound-target-key",
	"proxy-credentials-v1",
	"scout-message-key",
	"telegram-account-credentials-v1",
}

func TestImportFirstLaunchSeedRejectsEveryIncompleteOperationalSecretSet(t *testing.T) {
	for mask := 0; mask < (1<<len(firstLaunchRequiredSecretNamesForTest))-1; mask++ {
		t.Run(fmt.Sprintf("subset_%04b", mask), func(t *testing.T) {
			valid := completeFirstLaunchOperationalSecretsForTest()
			secrets := make(map[string][]byte)
			for index, name := range firstLaunchRequiredSecretNamesForTest {
				if mask&(1<<index) != 0 {
					secrets[name] = append([]byte(nil), valid[name]...)
				}
			}
			assertFirstLaunchSecretContractRejected(t, secrets)
		})
	}
}

func TestImportFirstLaunchSeedRejectsForbiddenFifthOperationalSecret(t *testing.T) {
	secrets := completeFirstLaunchOperationalSecretsForTest()
	secrets["backup-recovery-key-v1"] = firstLaunchSecretBytesForTest(0x55)
	assertFirstLaunchSecretContractRejected(t, secrets)
}

func TestImportFirstLaunchSeedRejectsEveryWrongOperationalSecretLengthBeforeKeychain(t *testing.T) {
	for _, name := range firstLaunchRequiredSecretNamesForTest {
		for _, length := range []int{31, 33} {
			t.Run(fmt.Sprintf("%s/%d_bytes", name, length), func(t *testing.T) {
				secrets := completeFirstLaunchOperationalSecretsForTest()
				secrets[name] = bytes.Repeat([]byte{0xa5}, length)
				assertFirstLaunchSecretContractRejected(t, secrets)
			})
		}
	}
}

func TestFirstLaunchSecretStagingRejectsEmptyOperationalSecrets(t *testing.T) {
	for _, emptyName := range firstLaunchRequiredSecretNamesForTest {
		t.Run(emptyName, func(t *testing.T) {
			staged := newFirstLaunchSecretStaging()
			if err := staged.Set(context.Background(), emptyName, nil); !errors.Is(err, ErrInvalidFirstLaunchSeed) {
				t.Fatalf("Set() error = %v, want ErrInvalidFirstLaunchSeed", err)
			}
			if len(staged.values) != 0 {
				t.Fatalf("empty operational secret was retained: %v", staged.values)
			}
		})
	}
}

func assertFirstLaunchSecretContractRejected(t *testing.T, secrets map[string][]byte) {
	t.Helper()
	targetRoot := t.TempDir()
	bundlePath := packFirstLaunchSeedWithSecrets(t, map[string]string{
		"app.db": "database-snapshot",
	}, secrets)
	prior := completeFirstLaunchOperationalSecretsWithOverridesForTest(map[string][]byte{
		"outbound-target-key":             bytes.Repeat([]byte{0x11}, 32),
		"proxy-credentials-v1":            bytes.Repeat([]byte{0x22}, 32),
		"scout-message-key":               bytes.Repeat([]byte{0x33}, 32),
		"telegram-account-credentials-v1": bytes.Repeat([]byte{0x44}, 32),
	})
	prior["unrelated-secret"] = []byte("preserve-unrelated")
	writer := &firstLaunchTestSecretWriter{values: cloneFirstLaunchSecretsForTest(prior)}

	imported, err := ImportFirstLaunchSeed(context.Background(), FirstLaunchSeedConfig{
		BundlePath: bundlePath,
		TargetRoot: targetRoot,
		AppVersion: "0.8.2",
		License:    "recipient-license",
		Secrets:    writer,
	})

	if imported {
		t.Fatal("ImportFirstLaunchSeed() imported a bundle with an invalid operational secret set")
	}
	if !errors.Is(err, ErrInvalidFirstLaunchSeed) {
		t.Fatalf("ImportFirstLaunchSeed() error = %v, want ErrInvalidFirstLaunchSeed", err)
	}
	for _, path := range []string{"data", "bootstrap-state"} {
		if _, statErr := os.Lstat(filepath.Join(targetRoot, path)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s retained after invalid operational secret set: %v", path, statErr)
		}
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if !reflect.DeepEqual(writer.values, prior) {
		t.Fatalf("stored secrets changed after invalid set\nwant: %v\ngot:  %v", prior, writer.values)
	}
	if writer.batchCalls != 0 {
		t.Fatalf("atomic keychain writes = %d, want zero", writer.batchCalls)
	}
}

func completeFirstLaunchOperationalSecretsForTest() map[string][]byte {
	secrets := make(map[string][]byte, len(firstLaunchRequiredSecretNamesForTest))
	for index, name := range firstLaunchRequiredSecretNamesForTest {
		secrets[name] = firstLaunchSecretBytesForTest(byte(index + 1))
	}
	return secrets
}

func firstLaunchSecretBytesForTest(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, 32)
}

func completeFirstLaunchOperationalSecretsWithOverridesForTest(overrides map[string][]byte) map[string][]byte {
	secrets := completeFirstLaunchOperationalSecretsForTest()
	for name, value := range overrides {
		secrets[name] = append([]byte(nil), value...)
	}
	return secrets
}
