//go:build darwin

package backup

import (
	"os"
	"path/filepath"
	"strings"
)

var trustedDarwinSystemAliases = []struct {
	alias  string
	target string
}{
	{alias: "/var", target: "/private/var"},
	{alias: "/tmp", target: "/private/tmp"},
	{alias: "/etc", target: "/private/etc"},
}

func normalizeTrustedSystemPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	for _, trusted := range trustedDarwinSystemAliases {
		if cleaned != trusted.alias && !strings.HasPrefix(cleaned, trusted.alias+string(os.PathSeparator)) {
			continue
		}
		info, err := os.Lstat(trusted.alias)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return cleaned, nil
		}
		resolved, err := filepath.EvalSymlinks(trusted.alias)
		if err != nil {
			return "", err
		}
		if resolved != trusted.target {
			return "", ErrUnsafePath
		}
		remainder := strings.TrimPrefix(cleaned, trusted.alias)
		return filepath.Join(trusted.target, remainder), nil
	}
	return cleaned, nil
}
