//go:build !darwin || ios

package license

import "errors"

func platformMachineIdentifier() (string, error) {
	return "", errors.New("platform identifier unavailable")
}
