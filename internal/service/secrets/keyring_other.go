//go:build !darwin

package secrets

import keyring "github.com/zalando/go-keyring"

func readSystemKeyring(service, name string) (string, error) {
	return keyring.Get(service, name)
}
