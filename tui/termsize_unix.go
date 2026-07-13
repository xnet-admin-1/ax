//go:build !windows

package tui

import (
	"os"

	"golang.org/x/sys/unix"
)

func getTerminalSize() (int, int) {
	if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil {
		return int(ws.Col), int(ws.Row)
	}
	return 80, 24
}
