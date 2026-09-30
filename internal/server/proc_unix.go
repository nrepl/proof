//go:build unix

package server

import (
	"os/exec"
	"syscall"
)

// Launchers like the Clojure CLI or rebar3 scripts spawn the real server
// as a child, so signals go to the whole process group.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminate(cmd *exec.Cmd) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

func kill(cmd *exec.Cmd) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
