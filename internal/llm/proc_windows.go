//go:build windows

package llm

import "os/exec"

// IsolateCmd is a no-op on Windows (SysProcAttr.Setpgid is Unix-only).
func IsolateCmd(cmd *exec.Cmd) {}
