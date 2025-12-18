//go:build windows
// +build windows

// SPDX-License-Identifier: MIT
// Original Author: Jianhui Zhao <zhaojh329@gmail.com>
// Modified for termix-agent

package main

import (
	"context"
	"os"
	"sync"

	conpty "github.com/qsocket/conpty-go"
)

type Terminal struct {
	pty       *conpty.ConPty
	mu        sync.Mutex
	closeOnce sync.Once
	closed    bool
}

// NewTerminal creates a new ConPTY terminal session on Windows.
// The username parameter is currently ignored on Windows.
func NewTerminal(username string) (*Terminal, error) {
	// Use PowerShell if available, otherwise cmd.exe
	shell := "cmd.exe"
	if _, err := os.Stat(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`); err == nil {
		shell = "powershell.exe"
	}

	pty, err := conpty.Start(shell)
	if err != nil {
		return nil, err
	}

	t := &Terminal{
		pty: pty,
	}

	// Monitor for process exit
	go func() {
		pty.Wait(context.Background())
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
	}()

	return t, nil
}

func (t *Terminal) Read(buf []byte) (int, error) {
	return t.pty.Read(buf)
}

func (t *Terminal) Write(data []byte) (int, error) {
	return t.pty.Write(data)
}

func (t *Terminal) SetWinSize(cols, rows uint16) error {
	return t.pty.Resize(int(cols), int(rows))
}

func (t *Terminal) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		t.pty.Close()
	})
	return nil
}

func (t *Terminal) IsClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}
