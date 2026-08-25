//go:build windows

package awscli

import (
	"os"
	"os/exec"
)

func configureDetached(command *exec.Cmd) {}

func stopProcess(command *exec.Cmd) error { return command.Process.Signal(os.Interrupt) }

func forceKillProcess(command *exec.Cmd) { _ = command.Process.Kill() }
