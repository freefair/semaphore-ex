//go:build windows

package db_lib

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func prepareTerraformCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

func interruptTerraformCommand(cmd *exec.Cmd) error {
	// Windows delivers CTRL_BREAK as os.Interrupt to Go programs. A process
	// group prevents the event from interrupting the Semaphore server itself.
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}
