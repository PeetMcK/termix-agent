//go:build windows
// +build windows

// SPDX-License-Identifier: MIT
// Original Author: Jianhui Zhao <zhaojh329@gmail.com>
// Modified for termix-agent

package main

import (
	"os/exec"
	"os/user"
)

// setSysProcAttr is a no-op on Windows
// Running as a different user requires different mechanisms on Windows
func setSysProcAttr(cmd *exec.Cmd, u *user.User) {
	// Not implemented on Windows
}
