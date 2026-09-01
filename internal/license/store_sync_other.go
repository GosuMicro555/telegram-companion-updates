//go:build !(aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris)

package license

func syncDirectory(string) error {
	return nil
}
