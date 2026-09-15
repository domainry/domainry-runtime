//go:build !windows

package codingruntime

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessGroup(process *os.Process, force bool) error {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	if err := syscall.Kill(-process.Pid, signal); err == nil {
		return nil
	}
	if force {
		return process.Kill()
	}
	return process.Signal(signal)
}
