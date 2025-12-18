//go:build !windows
// +build !windows

// SPDX-License-Identifier: MIT
// Original Author: Jianhui Zhao <zhaojh329@gmail.com>
// Modified for termix-agent

package main

import (
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

// setSysProcAttr sets the user/group credentials for command execution on Unix
func setSysProcAttr(cmd *exec.Cmd, u *user.User) {
	if u == nil {
		return
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return
	}

	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: uint32(uid),
			Gid: uint32(gid),
		},
	}
}
