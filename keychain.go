// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "termix-agent"
)

// StoredCredentials holds persisted agent credentials
type StoredCredentials struct {
	ServerAddr string `json:"serverAddr"`
	AgentToken string `json:"agentToken"`
	AgentID    string `json:"agentId"`
	DeviceID   string `json:"deviceId"`
	SSL        bool   `json:"ssl"`
}

// SaveCredentials stores agent credentials in OS keychain
func SaveCredentials(creds *StoredCredentials) error {
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}

	username := getKeychainUser()
	return keyring.Set(keychainService, username, string(data))
}

// LoadCredentials retrieves agent credentials from OS keychain
func LoadCredentials() (*StoredCredentials, error) {
	username := getKeychainUser()
	data, err := keyring.Get(keychainService, username)
	if err != nil {
		return nil, err
	}

	var creds StoredCredentials
	if err := json.Unmarshal([]byte(data), &creds); err != nil {
		return nil, err
	}

	return &creds, nil
}

// DeleteCredentials removes stored credentials
func DeleteCredentials() error {
	username := getKeychainUser()
	return keyring.Delete(keychainService, username)
}

// HasStoredCredentials checks if credentials exist
func HasStoredCredentials() bool {
	_, err := LoadCredentials()
	return err == nil
}

func getKeychainUser() string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "default"
	}
	return hostname
}
