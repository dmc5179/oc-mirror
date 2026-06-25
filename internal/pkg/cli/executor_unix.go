//go:build !windows

package cli

import "syscall"

// checkUmask verifies the current umask for file creation permissions.
// This is a Unix-specific concern (OCPBUGS-55374).
func checkUmask() (int, error) {
	currentUmask := syscall.Umask(0)
	syscall.Umask(currentUmask)
	return currentUmask, nil
}
