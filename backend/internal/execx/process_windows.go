//go:build windows

package execx

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNoWindow = 0x08000000

// prepareCommand hides the console window of child processes.
func prepareCommand(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.HideWindow = true
	command.SysProcAttr.CreationFlags |= createNoWindow
}

// killProcessTree terminates a process together with its descendants. `wslc`
// spawns helper processes, so plain Process.Kill would leave them behind
// (contract section 4.5).
func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	command := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = command.Run()
}

// detachedProcessAttributes is used when launching terminals/file explorers.
// These must be visible, so no console hiding applies.
func detachedProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
