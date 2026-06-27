// SPDX-License-Identifier: BUSL-1.1
// Copyright (c) 2026 xnet-admin-1
//
// Use of this source code is governed by the Business Source License
// included in the LICENSE file.

package debug

import (
	"fmt"
	"os"
	"sync"
	"time"
)

type Level int

const (
	Off     Level = 0
	Error   Level = 1
	Warning Level = 2
	Info    Level = 3
	Verbose Level = 4
)

var D = &Logger{level: Off}

type Logger struct {
	mu    sync.Mutex
	level Level
	file  *os.File
}

func (d *Logger) SetLevel(l Level) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.level = l
	if l > Off && d.file == nil {
		d.file, _ = os.OpenFile("/tmp/ax-debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	}
}

func (d *Logger) Level() Level {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.level
}

func (d *Logger) Enabled() bool { return d.Level() > Off }

func (d *Logger) log(lvl Level, prefix, format string, args ...any) {
	if d.Level() < lvl {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(d.file, "%s [%s] %s\n", time.Now().Format("15:04:05.000"), prefix, msg)
}

func (d *Logger) Error(format string, args ...any)   { d.log(Error, "ERR", format, args...) }
func (d *Logger) Warn(format string, args ...any)    { d.log(Warning, "WRN", format, args...) }
func (d *Logger) Info(format string, args ...any)    { d.log(Info, "INF", format, args...) }
func (d *Logger) Verbose(format string, args ...any) { d.log(Verbose, "VRB", format, args...) }

func (d *Logger) LevelName() string {
	switch d.Level() {
	case Off:
		return "off"
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Info:
		return "info"
	case Verbose:
		return "verbose"
	}
	return "?"
}
