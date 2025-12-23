// SPDX-License-Identifier: MIT

package main

import (
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 64 * 1024

	// Reconnection backoff
	minReconnectDelay = 5 * time.Second
	maxReconnectDelay = 60 * time.Second
)

// Agent is the main termix agent that manages WebSocket connection
type Agent struct {
	config    *Config
	conn      *websocket.Conn
	connMu    sync.Mutex
	sessions  *SessionManager
	cmdExec   *CommandExecutor
	fileOps   *FileOps
	startTime time.Time
	stopChan  chan struct{}
	wg        sync.WaitGroup
}

// NewAgent creates a new agent instance
func NewAgent(config *Config) *Agent {
	a := &Agent{
		config:    config,
		startTime: time.Now(),
		stopChan:  make(chan struct{}),
	}

	// Initialize session manager with callbacks
	a.sessions = NewSessionManager(
		a.sendPtyData,
		a.sendPtyExit,
	)

	// Initialize command executor with callbacks
	a.cmdExec = NewCommandExecutor(
		a.sendCmdResult,
		a.sendCmdError,
	)

	// Initialize file operations handler
	a.fileOps = NewFileOps(func(msgType string, data interface{}) {
		if err := a.sendMessage(msgType, data); err != nil {
			log.Error().Err(err).Str("type", msgType).Msg("failed to send file operation result")
		}
	})

	return a
}

// Run starts the agent and maintains connection
func (a *Agent) Run() error {
	reconnectDelay := minReconnectDelay

	for {
		select {
		case <-a.stopChan:
			return nil
		default:
		}

		err := a.connect()
		if err != nil {
			log.Error().Err(err).Msg("connection failed")

			if !a.config.Reconnect {
				return err
			}

			log.Info().Dur("delay", reconnectDelay).Msg("reconnecting")
			time.Sleep(reconnectDelay)

			// Exponential backoff
			reconnectDelay = reconnectDelay * 2
			if reconnectDelay > maxReconnectDelay {
				reconnectDelay = maxReconnectDelay
			}
			continue
		}

		// Reset backoff on successful connection
		reconnectDelay = minReconnectDelay

		// Run main loop
		err = a.mainLoop()
		if err != nil {
			log.Error().Err(err).Msg("connection lost")
		}

		// Cleanup
		a.connMu.Lock()
		if a.conn != nil {
			a.conn.Close()
			a.conn = nil
		}
		a.connMu.Unlock()

		a.sessions.CloseAllSessions()

		if !a.config.Reconnect {
			return err
		}

		log.Info().Dur("delay", reconnectDelay).Msg("reconnecting")
		time.Sleep(reconnectDelay)
	}
}

// Stop gracefully stops the agent
func (a *Agent) Stop() {
	close(a.stopChan)
	a.sessions.CloseAllSessions()

	a.connMu.Lock()
	if a.conn != nil {
		a.conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		a.conn.Close()
	}
	a.connMu.Unlock()

	a.wg.Wait()
}

// connect establishes WebSocket connection
func (a *Agent) connect() error {
	url := a.config.WebSocketURL()
	log.Info().Str("url", url).Msg("connecting to server")

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	if a.config.SSL {
		dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: a.config.Insecure,
		}
	}

	header := http.Header{}
	if a.config.Token != "" {
		header.Set("Authorization", "Bearer "+a.config.Token)
	}

	conn, _, err := dialer.Dial(url, header)
	if err != nil {
		return err
	}

	a.connMu.Lock()
	a.conn = conn
	a.connMu.Unlock()

	// Send registration
	if err := a.sendRegistration(); err != nil {
		conn.Close()
		return err
	}

	log.Info().Str("deviceId", a.config.DeviceID).Msg("connected and registered")
	return nil
}

// sendRegistration sends the initial registration message
func (a *Agent) sendRegistration() error {
	hostname, _ := os.Hostname()

	data := RegisterData{
		DeviceID:  a.config.DeviceID,
		Token:     a.config.Token,
		Hostname:  hostname,
		Platform:  Platform(),
		OS:        OSInfo(),
		Arch:      Arch(),
		GoVersion: runtime.Version(),
	}

	return a.sendMessage(MsgTypeRegister, data)
}

// mainLoop handles message reading and heartbeats
func (a *Agent) mainLoop() error {
	a.conn.SetReadLimit(maxMessageSize)
	a.conn.SetReadDeadline(time.Now().Add(pongWait))
	a.conn.SetPongHandler(func(string) error {
		a.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Start heartbeat goroutine
	a.wg.Add(1)
	go a.heartbeatLoop()

	for {
		select {
		case <-a.stopChan:
			return nil
		default:
		}

		_, message, err := a.conn.ReadMessage()
		if err != nil {
			return err
		}

		if err := a.handleMessage(message); err != nil {
			log.Error().Err(err).Msg("failed to handle message")
		}
	}
}

// heartbeatLoop sends periodic heartbeats
func (a *Agent) heartbeatLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(time.Duration(a.config.Heartbeat) * time.Second)
	defer ticker.Stop()

	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()

	for {
		select {
		case <-a.stopChan:
			return
		case <-ticker.C:
			uptime := int64(time.Since(a.startTime).Seconds())
			if err := a.sendMessage(MsgTypeHeartbeat, HeartbeatData{Uptime: uptime}); err != nil {
				log.Error().Err(err).Msg("failed to send heartbeat")
				return
			}
		case <-pingTicker.C:
			a.connMu.Lock()
			if a.conn != nil {
				a.conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := a.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					a.connMu.Unlock()
					log.Error().Err(err).Msg("failed to send ping")
					return
				}
			}
			a.connMu.Unlock()
		}
	}
}

// handleMessage dispatches incoming messages
func (a *Agent) handleMessage(data []byte) error {
	msg, err := ParseMessage(data)
	if err != nil {
		return err
	}

	log.Debug().Str("type", msg.Type).Msg("received message")

	switch msg.Type {
	case MsgTypeRegisterAck:
		return a.handleRegisterAck(msg)
	case MsgTypeSpawnPty:
		return a.handleSpawnPty(msg)
	case MsgTypePtyInput:
		return a.handlePtyInput(msg)
	case MsgTypePtyResize:
		return a.handlePtyResize(msg)
	case MsgTypeClosePty:
		return a.handleClosePty(msg)
	case MsgTypeExecCmd:
		return a.handleExecCmd(msg)
	case MsgTypePing:
		return a.sendMessage(MsgTypePong, nil)

	// File operations
	case MsgTypeListFiles:
		return a.handleListFiles(msg)
	case MsgTypeDownloadFile:
		return a.handleDownloadFile(msg)
	case MsgTypeUploadFile:
		return a.handleUploadFile(msg)
	case MsgTypeCreateFile:
		return a.handleCreateFile(msg)
	case MsgTypeCreateFolder:
		return a.handleCreateFolder(msg)
	case MsgTypeDeleteItem:
		return a.handleDeleteItem(msg)
	case MsgTypeCopyItem:
		return a.handleCopyItem(msg)
	case MsgTypeMoveItem:
		return a.handleMoveItem(msg)
	case MsgTypeRenameItem:
		return a.handleRenameItem(msg)

	// Streaming file operations
	case MsgTypeStreamFileInfo:
		return a.handleStreamFileInfo(msg)
	case MsgTypeStreamChunk:
		return a.handleStreamChunk(msg)

	case MsgTypeCompressFiles:
		return a.handleCompressFiles(msg)

	case MsgTypeGetDirStats:
		return a.handleGetDirStats(msg)

	default:
		log.Warn().Str("type", msg.Type).Msg("unknown message type")
	}

	return nil
}

// --- Message handlers ---

func (a *Agent) handleRegisterAck(msg *Message) error {
	data, err := UnmarshalData[RegisterAckData](msg)
	if err != nil {
		return err
	}

	if !data.Success {
		log.Error().Str("message", data.Message).Msg("registration failed")
	} else {
		log.Info().Msg("registration acknowledged")
	}

	return nil
}

func (a *Agent) handleSpawnPty(msg *Message) error {
	data, err := UnmarshalData[SpawnPtyData](msg)
	if err != nil {
		return err
	}

	log.Info().
		Str("sessionId", data.SessionID).
		Uint16("cols", data.Cols).
		Uint16("rows", data.Rows).
		Msg("spawn PTY request")

	if err := a.sessions.SpawnSession(data.SessionID, data.Cols, data.Rows, data.Username); err != nil {
		log.Error().Err(err).Str("sessionId", data.SessionID).Msg("failed to spawn PTY")
		// Notify server of failure
		a.sendPtyExit(data.SessionID, -1)
	}

	return nil
}

func (a *Agent) handlePtyInput(msg *Message) error {
	data, err := UnmarshalData[PtyInputData](msg)
	if err != nil {
		return err
	}

	session, err := a.sessions.GetSession(data.SessionID)
	if err != nil {
		return err
	}

	return session.WriteBase64(data.Data)
}

func (a *Agent) handlePtyResize(msg *Message) error {
	data, err := UnmarshalData[PtyResizeData](msg)
	if err != nil {
		return err
	}

	session, err := a.sessions.GetSession(data.SessionID)
	if err != nil {
		return err
	}

	return session.Resize(data.Cols, data.Rows)
}

func (a *Agent) handleClosePty(msg *Message) error {
	data, err := UnmarshalData[ClosePtyData](msg)
	if err != nil {
		return err
	}

	return a.sessions.CloseSession(data.SessionID)
}

func (a *Agent) handleExecCmd(msg *Message) error {
	data, err := UnmarshalData[ExecCmdData](msg)
	if err != nil {
		return err
	}

	log.Info().
		Str("token", data.Token).
		Str("command", data.Command).
		Strs("args", data.Args).
		Msg("exec command request")

	a.cmdExec.Execute(data)
	return nil
}

// --- Outgoing message helpers ---

func (a *Agent) sendMessage(msgType string, data interface{}) error {
	msg, err := MarshalMessage(msgType, data)
	if err != nil {
		return err
	}

	a.connMu.Lock()
	defer a.connMu.Unlock()

	if a.conn == nil {
		return websocket.ErrCloseSent
	}

	a.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return a.conn.WriteMessage(websocket.TextMessage, msg)
}

func (a *Agent) sendPtyData(sessionID string, data []byte) {
	msg := PtyDataMsg{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString(data),
	}

	if err := a.sendMessage(MsgTypePtyData, msg); err != nil {
		log.Error().Err(err).Str("sessionId", sessionID).Msg("failed to send PTY data")
	}
}

func (a *Agent) sendPtyExit(sessionID string, code int) {
	msg := PtyExitMsg{
		SessionID: sessionID,
		Code:      code,
	}

	if err := a.sendMessage(MsgTypePtyExit, msg); err != nil {
		log.Error().Err(err).Str("sessionId", sessionID).Msg("failed to send PTY exit")
	}
}

func (a *Agent) sendCmdResult(result *CmdResultData) {
	if err := a.sendMessage(MsgTypeCmdResult, result); err != nil {
		log.Error().Err(err).Str("token", result.Token).Msg("failed to send command result")
	}
}

func (a *Agent) sendCmdError(token string, code int, message string) {
	msg := CmdErrorData{
		Token:   token,
		Code:    code,
		Message: message,
	}

	if err := a.sendMessage(MsgTypeCmdError, msg); err != nil {
		log.Error().Err(err).Str("token", token).Msg("failed to send command error")
	}
}

// --- File operation handlers ---

func (a *Agent) handleListFiles(msg *Message) error {
	data, err := UnmarshalData[ListFilesData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("path", data.Path).Msg("list files request")
	go a.fileOps.ListFiles(data)
	return nil
}

func (a *Agent) handleDownloadFile(msg *Message) error {
	data, err := UnmarshalData[DownloadFileData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("path", data.Path).Msg("download file request")
	go a.fileOps.DownloadFile(data)
	return nil
}

func (a *Agent) handleUploadFile(msg *Message) error {
	data, err := UnmarshalData[UploadFileData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("path", data.Path).Str("fileName", data.FileName).Msg("upload file request")
	go a.fileOps.UploadFile(data)
	return nil
}

func (a *Agent) handleCreateFile(msg *Message) error {
	data, err := UnmarshalData[CreateFileData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("path", data.Path).Str("fileName", data.FileName).Msg("create file request")
	go a.fileOps.CreateFile(data)
	return nil
}

func (a *Agent) handleCreateFolder(msg *Message) error {
	data, err := UnmarshalData[CreateFolderData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("path", data.Path).Str("folderName", data.FolderName).Msg("create folder request")
	go a.fileOps.CreateFolder(data)
	return nil
}

func (a *Agent) handleDeleteItem(msg *Message) error {
	data, err := UnmarshalData[DeleteItemData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("path", data.Path).Bool("isDirectory", data.IsDirectory).Msg("delete item request")
	go a.fileOps.DeleteItem(data)
	return nil
}

func (a *Agent) handleCopyItem(msg *Message) error {
	data, err := UnmarshalData[CopyItemData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("source", data.SourcePath).Str("target", data.TargetDir).Msg("copy item request")
	go a.fileOps.CopyItem(data)
	return nil
}

func (a *Agent) handleMoveItem(msg *Message) error {
	data, err := UnmarshalData[MoveItemData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("source", data.SourcePath).Str("target", data.TargetPath).Msg("move item request")
	go a.fileOps.MoveItem(data)
	return nil
}

func (a *Agent) handleRenameItem(msg *Message) error {
	data, err := UnmarshalData[RenameItemData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("path", data.Path).Str("newName", data.NewName).Msg("rename item request")
	go a.fileOps.RenameItem(data)
	return nil
}

func (a *Agent) handleStreamFileInfo(msg *Message) error {
	data, err := UnmarshalData[StreamFileInfoData](msg)
	if err != nil {
		return err
	}

	log.Info().Str("path", data.Path).Msg("stream file info request")
	go a.fileOps.StreamFileInfo(data)
	return nil
}

func (a *Agent) handleStreamChunk(msg *Message) error {
	data, err := UnmarshalData[StreamChunkData](msg)
	if err != nil {
		return err
	}

	log.Debug().
		Str("path", data.Path).
		Int64("offset", data.Offset).
		Int64("length", data.Length).
		Msg("stream chunk request")
	go a.fileOps.StreamChunk(data)
	return nil
}

func (a *Agent) handleCompressFiles(msg *Message) error {
	data, err := UnmarshalData[CompressFilesData](msg)
	if err != nil {
		return err
	}

	log.Debug().
		Strs("paths", data.Paths).
		Str("archiveName", data.ArchiveName).
		Str("format", data.Format).
		Msg("compress files request")
	go a.fileOps.CompressFiles(data)
	return nil
}

func (a *Agent) handleGetDirStats(msg *Message) error {
	data, err := UnmarshalData[GetDirStatsData](msg)
	if err != nil {
		return err
	}

	log.Debug().
		Str("path", data.Path).
		Msg("get dir stats request")
	go a.fileOps.GetDirStats(data)
	return nil
}
