//go:build windows

package tui

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

type coord struct {
	x, y int16
}

type smallRect struct {
	left, top, right, bottom int16
}

type consoleScreenBufferInfo struct {
	size       coord
	cursorPos  coord
	attrs      uint16
	window     smallRect
	maxWinSize coord
}

func getTerminalSize() (int, int) {
	h := syscall.Handle(os.Stdout.Fd())
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 80, 24
	}
	w := int(info.window.right - info.window.left + 1)
	h2 := int(info.window.bottom - info.window.top + 1)
	if w <= 0 {
		w = 80
	}
	if h2 <= 0 {
		h2 = 24
	}
	return w, h2
}
