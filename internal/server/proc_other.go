//go:build !unix

package server

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func terminate(cmd *exec.Cmd) { cmd.Process.Kill() }

func kill(cmd *exec.Cmd) { cmd.Process.Kill() }
