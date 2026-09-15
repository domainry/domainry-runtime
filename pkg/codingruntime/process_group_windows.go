//go:build windows

package codingruntime

import (
	"os"
	"os/exec"
)

func configureProcessGroup(_ *exec.Cmd) {}

func terminateProcessGroup(process *os.Process, _ bool) error {
	return process.Kill()
}
