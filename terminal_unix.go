//go:build !windows
// +build !windows

// SPDX-License-Identifier: MIT
// Original Author: Jianhui Zhao <zhaojh329@gmail.com>
// Modified for termix-agent

package main

import (
	"os"
	"os/exec"
	"os/user"
	"sync"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
)

type Terminal struct {
	pty *os.File
	cmd *exec.Cmd
	mu  sync.Mutex
}

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// NewTerminal creates a new PTY terminal session.
// If username is provided, attempts to run as that user's shell.
// Otherwise uses the current user's default shell.
func NewTerminal(username string) (*Terminal, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	var cmd *exec.Cmd

	if username != "" {
		// Try to get the user's shell
		u, err := user.Lookup(username)
		if err == nil && u.HomeDir != "" {
			// Use login shell for the specified user
			cmd = exec.Command(shell, "-l")
			cmd.Env = append(os.Environ(),
				"HOME="+u.HomeDir,
				"USER="+username,
				"LOGNAME="+username,
			)
			cmd.Dir = u.HomeDir
		} else {
			cmd = exec.Command(shell, "-l")
		}
	} else {
		cmd = exec.Command(shell, "-l")
	}

	// Set TERM environment variable
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	t := &Terminal{
		pty: ptmx,
		cmd: cmd,
	}

	return t, nil
}

func (t *Terminal) Read(buf []byte) (int, error) {
	return t.pty.Read(buf)
}

func (t *Terminal) Write(data []byte) (int, error) {
	return t.pty.Write(data)
}

func (t *Terminal) SetWinSize(cols, rows uint16) error {
	ws := &winsize{
		Row: rows,
		Col: cols,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		t.pty.Fd(),
		uintptr(syscall.TIOCSWINSZ),
		uintptr(unsafe.Pointer(ws)),
	)
	if errno != 0 {
		return errno
	}

	return nil
}

func (t *Terminal) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}

	if t.pty != nil {
		return t.pty.Close()
	}

	return nil
}
