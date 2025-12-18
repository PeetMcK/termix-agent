// SPDX-License-Identifier: MIT

package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// EnrollConfig holds enrollment configuration
type EnrollConfig struct {
	Server   string
	Token    string
	DeviceID string
	SSL      bool
	Insecure bool
}

// EnrollAckData is the server response to enrollment
type EnrollAckData struct {
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
	AgentID    string `json:"agentId,omitempty"`
	AgentToken string `json:"agentToken,omitempty"`
	Config     struct {
		EnableTerminal    bool `json:"enableTerminal"`
		EnableFileManager bool `json:"enableFileManager"`
		EnableTunnels     bool `json:"enableTunnels"`
	} `json:"config,omitempty"`
}

// Enroll connects to server with install token and retrieves agent token
func Enroll(cfg *EnrollConfig) error {
	if cfg.DeviceID == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "unknown"
		}
		cfg.DeviceID = hostname
	}

	scheme := "ws"
	if cfg.SSL {
		scheme = "wss"
	}
	url := fmt.Sprintf("%s://%s/ws/agent", scheme, cfg.Server)

	log.Info().Str("server", cfg.Server).Bool("ssl", cfg.SSL).Msg("enrolling with server")

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	if cfg.SSL {
		dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: cfg.Insecure,
		}
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+cfg.Token)

	conn, _, err := dialer.Dial(url, header)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	// Send registration with install token
	hostname, _ := os.Hostname()
	regData := RegisterData{
		DeviceID:  cfg.DeviceID,
		Token:     cfg.Token,
		Hostname:  hostname,
		Platform:  Platform(),
		OS:        OSInfo(),
		Arch:      Arch(),
		GoVersion: runtime.Version(),
	}

	msg, err := MarshalMessage(MsgTypeRegister, regData)
	if err != nil {
		return fmt.Errorf("failed to marshal registration: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
		return fmt.Errorf("failed to send registration: %w", err)
	}

	// Wait for response
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, respData, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var respMsg Message
	if err := json.Unmarshal(respData, &respMsg); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if respMsg.Type != MsgTypeRegisterAck {
		return fmt.Errorf("unexpected response type: %s", respMsg.Type)
	}

	var ackData EnrollAckData
	if err := json.Unmarshal(respMsg.Data, &ackData); err != nil {
		return fmt.Errorf("failed to parse ack data: %w", err)
	}

	if !ackData.Success {
		return fmt.Errorf("enrollment failed: %s", ackData.Message)
	}

	// Store credentials in keychain
	creds := &StoredCredentials{
		ServerAddr: cfg.Server,
		AgentToken: ackData.AgentToken,
		AgentID:    ackData.AgentID,
		DeviceID:   cfg.DeviceID,
		SSL:        cfg.SSL,
	}

	if err := SaveCredentials(creds); err != nil {
		return fmt.Errorf("failed to store credentials in keychain: %w", err)
	}

	log.Info().
		Str("agentId", ackData.AgentID).
		Str("server", cfg.Server).
		Msg("enrollment successful")

	fmt.Println("Enrollment successful!")
	fmt.Printf("Agent ID: %s\n", ackData.AgentID)
	fmt.Printf("Server: %s\n", cfg.Server)
	fmt.Println("\nCredentials stored in system keychain.")
	fmt.Println("Run 'termix-agent' to connect.")

	return nil
}
