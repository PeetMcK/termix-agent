// SPDX-License-Identifier: MIT
// Original Author: Jianhui Zhao <zhaojh329@gmail.com>
// Modified for termix-agent

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os/exec"
	"os/user"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	cmdRunningLimit        = 5
	cmdExecDefaultTimeout  = 30 * time.Second
	cmdExecMaxTimeout      = 600 * time.Second // 10 minutes max
)

// CmdError codes
const (
	CmdErrNone = iota
	CmdErrPermit
	CmdErrNotFound
	CmdErrNoMem
	CmdErrSysErr
	CmdErrRespTooBig
)

var cmdSemaphore = make(chan struct{}, cmdRunningLimit)

// CommandExecutor handles remote command execution
type CommandExecutor struct {
	sendResult func(result *CmdResultData)
	sendError  func(token string, code int, message string)
}

// NewCommandExecutor creates a new command executor
func NewCommandExecutor(
	sendResult func(result *CmdResultData),
	sendError func(token string, code int, message string),
) *CommandExecutor {
	return &CommandExecutor{
		sendResult: sendResult,
		sendError:  sendError,
	}
}

// Execute runs a command and sends the result via the callback
func (e *CommandExecutor) Execute(cmd *ExecCmdData) {
	// Validate user if specified
	var u *user.User
	var err error

	if cmd.Username != "" {
		u, err = user.Lookup(cmd.Username)
		if err != nil {
			log.Error().Err(err).Str("username", cmd.Username).Msg("user lookup failed")
			e.sendError(cmd.Token, CmdErrPermit, "operation not permitted")
			return
		}
	}

	// Find command path
	cmdPath, err := exec.LookPath(cmd.Command)
	if err != nil || cmdPath == "" {
		log.Error().Str("command", cmd.Command).Msg("command not found")
		e.sendError(cmd.Token, CmdErrNotFound, "command not found")
		return
	}

	// Determine timeout
	timeout := cmdExecDefaultTimeout
	if cmd.Timeout > 0 {
		timeout = time.Duration(cmd.Timeout) * time.Second
		if timeout > cmdExecMaxTimeout {
			timeout = cmdExecMaxTimeout
		}
	}

	// Try to acquire semaphore
	select {
	case cmdSemaphore <- struct{}{}:
		go e.executeCommand(u, cmdPath, cmd.Args, cmd.Token, timeout)
	default:
		log.Warn().Int("limit", cmdRunningLimit).Msg("command limit reached")
		e.sendError(cmd.Token, CmdErrNoMem, "too many concurrent commands")
	}
}

func (e *CommandExecutor) executeCommand(u *user.User, cmdPath string, args []string, token string, timeout time.Duration) {
	defer func() {
		<-cmdSemaphore
	}()

	log.Debug().Str("command", cmdPath).Strs("args", args).Str("token", token).Dur("timeout", timeout).Msg("executing command")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cmdPath, args...)

	// Set user credentials if specified (Unix only)
	if u != nil {
		setSysProcAttr(cmd, u)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode := 0
	err := cmd.Run()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Error().Str("command", cmdPath).Str("token", token).Msg("command timeout")
			e.sendError(token, CmdErrSysErr, "command timeout")
			return
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			log.Error().Err(err).Str("command", cmdPath).Str("token", token).Msg("command execution failed")
			e.sendError(token, CmdErrSysErr, err.Error())
			return
		}
	}

	stdoutBytes := stdout.Bytes()
	stderrBytes := stderr.Bytes()

	// Check response size limit (64KB)
	stdoutB64 := base64.StdEncoding.EncodeToString(stdoutBytes)
	stderrB64 := base64.StdEncoding.EncodeToString(stderrBytes)

	if len(stdoutB64)+len(stderrB64) > 65000 {
		e.sendError(token, CmdErrRespTooBig, "stdout+stderr is too big")
		return
	}

	e.sendResult(&CmdResultData{
		Token:    token,
		ExitCode: exitCode,
		Stdout:   stdoutB64,
		Stderr:   stderrB64,
	})
}

// CmdErrorString converts error code to string
func CmdErrorString(code int) string {
	switch code {
	case CmdErrPermit:
		return "operation not permitted"
	case CmdErrNotFound:
		return "command not found"
	case CmdErrNoMem:
		return "too many concurrent commands"
	case CmdErrSysErr:
		return "system error"
	case CmdErrRespTooBig:
		return "response too large"
	default:
		return ""
	}
}
