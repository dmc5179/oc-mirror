//go:build windows

package cli

// checkUmask is a no-op on Windows where umask doesn't exist.
// Windows uses ACLs for permission control instead of POSIX permissions.
// Returns the expected value (0o022) to satisfy the check without warnings.
func checkUmask() (int, error) {
	return 0o022, nil
}
