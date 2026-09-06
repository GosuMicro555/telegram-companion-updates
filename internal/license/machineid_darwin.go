//go:build darwin && !ios

package license

import (
	"errors"
	"os/exec"
	"regexp"
)

var ioPlatformUUID = regexp.MustCompile(`"IOPlatformUUID" = "([^"]+)"`)

func platformMachineIdentifier() (string, error) {
	output, err := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", errors.New("ioreg unavailable")
	}
	matches := ioPlatformUUID.FindSubmatch(output)
	if len(matches) != 2 {
		return "", errors.New("platform identifier unavailable")
	}
	return string(matches[1]), nil
}
