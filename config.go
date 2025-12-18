// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"runtime"
)

// Config holds the agent configuration
type Config struct {
	ServerAddr string // WebSocket server address (host:port)
	DeviceID   string // Unique device identifier
	Token      string // Authentication token
	SSL        bool   // Use TLS/SSL connection
	Insecure   bool   // Skip TLS certificate verification
	Reconnect  bool   // Auto-reconnect on disconnect
	Heartbeat  int    // Heartbeat interval in seconds
	Debug      bool   // Enable debug logging
}

// DefaultConfig returns configuration with sensible defaults
func DefaultConfig() *Config {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	return &Config{
		ServerAddr: "localhost:30007",
		DeviceID:   hostname,
		Token:      "",
		SSL:        true,
		Insecure:   false,
		Reconnect:  true,
		Heartbeat:  30,
		Debug:      false,
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.ServerAddr == "" {
		return fmt.Errorf("server address is required")
	}

	if c.DeviceID == "" {
		return fmt.Errorf("device ID is required")
	}

	if c.Heartbeat < 5 {
		c.Heartbeat = 5
	}

	if c.Heartbeat > 300 {
		c.Heartbeat = 300
	}

	return nil
}

// WebSocketURL returns the full WebSocket URL
func (c *Config) WebSocketURL() string {
	scheme := "ws"
	if c.SSL {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/ws/agent", scheme, c.ServerAddr)
}

// Platform returns the current platform string
func Platform() string {
	return runtime.GOOS
}

// Arch returns the current architecture string
func Arch() string {
	return runtime.GOARCH
}

// OSInfo returns OS version info
func OSInfo() string {
	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}
