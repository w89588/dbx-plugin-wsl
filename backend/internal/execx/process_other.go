//go:build !windows

package execx

import (
	"os"
	"os/exec"
	"syscall"
)

// Stubs so the packages can be type-checked on non-Windows hosts. The plugin
// only ships for Windows.

func prepareCommand(_ *exec.Cmd) {}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
	}
}

func detachedProcessAttributes() *syscall.SysProcAttr { return nil }
