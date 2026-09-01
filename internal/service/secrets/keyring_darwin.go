//go:build darwin

package secrets

import (
	"encoding/base64"
	"encoding/hex"
	"os/exec"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

const macOSSecurityPath = "/usr/bin/security"

type macOSKeychainRunner func(string, ...string) ([]byte, error)

func readSystemKeyring(service, name string) (string, error) {
	return readMacOSKeyring(func(command string, args ...string) ([]byte, error) {
		return exec.Command(command, args...).CombinedOutput()
	}, service, name)
}

func readMacOSKeyring(run macOSKeychainRunner, service, name string) (string, error) {
	out, err := run(macOSSecurityPath,
		"find-generic-password", "-s", service, "-a", name, "-w",
	)
	if err != nil {
		if strings.Contains(string(out), "could not be found") {
			return "", keyring.ErrNotFound
		}
		return "", err
	}

	value := strings.TrimSpace(string(out))
	if strings.HasPrefix(value, "go-keyring-base64:") {
		decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "go-keyring-base64:"))
		if decodeErr != nil {
			return "", decodeErr
		}
		return string(decoded), nil
	}
	if strings.HasPrefix(value, "go-keyring-encoded:") {
		decoded, decodeErr := hex.DecodeString(strings.TrimPrefix(value, "go-keyring-encoded:"))
		if decodeErr != nil {
			return "", decodeErr
		}
		return string(decoded), nil
	}
	return value, nil
}
