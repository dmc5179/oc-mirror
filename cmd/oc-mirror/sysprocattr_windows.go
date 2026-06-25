//go:build windows

package main

import "syscall"

// sysProcAttr creates a Windows-compatible SysProcAttr.
// Windows doesn't support Setpgid or Pdeathsig fields, so we return
// an empty SysProcAttr structure.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
