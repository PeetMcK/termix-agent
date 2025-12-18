// SPDX-License-Identifier: MIT

package main

import (
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	maxSessions        = 10
	sessionReadBufSize = 4096
	inactivityTimeout  = 600 * time.Second
)

var (
	ErrMaxSessions   = errors.New("maximum sessions reached")
	ErrSessionExists = errors.New("session already exists")
	ErrNoSession     = errors.New("session not found")
)

// SessionManager manages multiple PTY sessions
type SessionManager struct {
	sessions     sync.Map
	sessionCount int32
	sendData     func(sessionID string, data []byte)
	sendExit     func(sessionID string, code int)
}

// NewSessionManager creates a new session manager
func NewSessionManager(
	sendData func(sessionID string, data []byte),
	sendExit func(sessionID string, code int),
) *SessionManager {
	return &SessionManager{
		sendData: sendData,
		sendExit: sendExit,
	}
}

// TermSession wraps a Terminal with session metadata
type TermSession struct {
	ID           string
	terminal     *Terminal
	manager      *SessionManager
	lastActivity time.Time
	mu           sync.Mutex
	closed       bool
	stopChan     chan struct{}
}

// SpawnSession creates and starts a new PTY session
func (m *SessionManager) SpawnSession(sessionID string, cols, rows uint16, username string) error {
	// Check session limit
	if atomic.LoadInt32(&m.sessionCount) >= maxSessions {
		return ErrMaxSessions
	}

	// Check if session already exists
	if _, exists := m.sessions.Load(sessionID); exists {
		return ErrSessionExists
	}

	// Create terminal
	terminal, err := NewTerminal(username)
	if err != nil {
		return err
	}

	// Set initial window size
	if err := terminal.SetWinSize(cols, rows); err != nil {
		terminal.Close()
		return err
	}

	session := &TermSession{
		ID:           sessionID,
		terminal:     terminal,
		manager:      m,
		lastActivity: time.Now(),
		stopChan:     make(chan struct{}),
	}

	m.sessions.Store(sessionID, session)
	atomic.AddInt32(&m.sessionCount, 1)

	log.Info().
		Str("sessionId", sessionID).
		Uint16("cols", cols).
		Uint16("rows", rows).
		Str("username", username).
		Msg("session spawned")

	// Start read loop
	go session.readLoop()

	// Start inactivity monitor
	go session.inactivityMonitor()

	return nil
}

// GetSession retrieves a session by ID
func (m *SessionManager) GetSession(sessionID string) (*TermSession, error) {
	val, ok := m.sessions.Load(sessionID)
	if !ok {
		return nil, ErrNoSession
	}
	return val.(*TermSession), nil
}

// CloseSession closes and removes a session
func (m *SessionManager) CloseSession(sessionID string) error {
	val, ok := m.sessions.LoadAndDelete(sessionID)
	if !ok {
		return ErrNoSession
	}

	session := val.(*TermSession)
	session.Close()
	atomic.AddInt32(&m.sessionCount, -1)

	log.Info().Str("sessionId", sessionID).Msg("session closed")
	return nil
}

// CloseAllSessions closes all active sessions
func (m *SessionManager) CloseAllSessions() {
	m.sessions.Range(func(key, value interface{}) bool {
		sessionID := key.(string)
		session := value.(*TermSession)
		session.Close()
		m.sessions.Delete(sessionID)
		atomic.AddInt32(&m.sessionCount, -1)
		log.Debug().Str("sessionId", sessionID).Msg("session closed during cleanup")
		return true
	})
}

// SessionCount returns the number of active sessions
func (m *SessionManager) SessionCount() int {
	return int(atomic.LoadInt32(&m.sessionCount))
}

// Write sends input data to the terminal
func (s *TermSession) Write(data []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrNoSession
	}
	s.lastActivity = time.Now()
	s.mu.Unlock()

	_, err := s.terminal.Write(data)
	return err
}

// WriteBase64 decodes and sends base64 input to the terminal
func (s *TermSession) WriteBase64(b64data string) error {
	data, err := base64.StdEncoding.DecodeString(b64data)
	if err != nil {
		return err
	}
	return s.Write(data)
}

// Resize changes the terminal window size
func (s *TermSession) Resize(cols, rows uint16) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrNoSession
	}
	s.lastActivity = time.Now()
	s.mu.Unlock()

	return s.terminal.SetWinSize(cols, rows)
}

// Close terminates the session
func (s *TermSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.stopChan)
	s.mu.Unlock()

	s.terminal.Close()
}

// readLoop reads from the terminal and sends data to the server
func (s *TermSession) readLoop() {
	buf := make([]byte, sessionReadBufSize)

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		n, err := s.terminal.Read(buf)
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()

			if !closed {
				log.Debug().
					Err(err).
					Str("sessionId", s.ID).
					Msg("terminal read error, closing session")

				// Notify server of session exit
				s.manager.sendExit(s.ID, 0)

				// Remove from manager
				s.manager.sessions.Delete(s.ID)
				atomic.AddInt32(&s.manager.sessionCount, -1)
				s.Close()
			}
			return
		}

		if n > 0 {
			s.mu.Lock()
			s.lastActivity = time.Now()
			s.mu.Unlock()

			// Send data to server
			s.manager.sendData(s.ID, buf[:n])
		}
	}
}

// inactivityMonitor closes the session after inactivity timeout
func (s *TermSession) inactivityMonitor() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.mu.Lock()
			inactive := time.Since(s.lastActivity) > inactivityTimeout
			closed := s.closed
			s.mu.Unlock()

			if inactive && !closed {
				log.Info().
					Str("sessionId", s.ID).
					Dur("timeout", inactivityTimeout).
					Msg("session inactive, closing")

				s.manager.sendExit(s.ID, 0)
				s.manager.sessions.Delete(s.ID)
				atomic.AddInt32(&s.manager.sessionCount, -1)
				s.Close()
				return
			}
		}
	}
}
