//go:build !windows

package llm

import (
	"os/exec"
	"syscall"
)

// IsolateCmd configures a command for process isolation:
// - Starts in a new process group (prevents terminal signal bleed)
// - Cannot acquire a controlling terminal
func IsolateCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}
