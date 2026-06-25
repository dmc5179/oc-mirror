//go:build !linux && !windows

package main

import "syscall"

// sysProcAttr creates a Unix-compatible (non-Linux, non-Windows) SysProcAttr.
// We use Setpgid as true to tie the child process to the parent process.
// This is primarily for macOS and other Unix systems.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true,
	}
}
