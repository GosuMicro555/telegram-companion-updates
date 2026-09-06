//go:build !darwin

package backup

func normalizeTrustedSystemPath(path string) (string, error) {
	return path, nil
}
