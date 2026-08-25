//go:build unix

package awscli

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureDetached(command *exec.Cmd) {
	// A separate process group prevents the TUI terminal's foreground signals
	// from accidentally terminating an independently tracked tunnel.
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func stopProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGINT); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

func forceKillProcess(command *exec.Cmd) {
	if command.Process != nil {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
}
