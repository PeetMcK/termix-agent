// SPDX-License-Identifier: MIT

package main

import "encoding/json"

// Message types
const (
	// Agent → Server
	MsgTypeRegister  = "register"
	MsgTypeHeartbeat = "heartbeat"
	MsgTypePtyData   = "pty_data"
	MsgTypePtyExit   = "pty_exit"
	MsgTypeCmdResult = "cmd_result"
	MsgTypeCmdError  = "cmd_error"
	MsgTypePong      = "pong"

	// File operation responses (Agent → Server)
	MsgTypeFileList     = "file_list"
	MsgTypeFileContent  = "file_content"
	MsgTypeFileOpResult = "file_op_result"
	MsgTypeFileError    = "file_error"

	// Server → Agent
	MsgTypeRegisterAck = "register_ack"
	MsgTypeSpawnPty    = "spawn_pty"
	MsgTypePtyInput    = "pty_input"
	MsgTypePtyResize   = "pty_resize"
	MsgTypeClosePty    = "close_pty"
	MsgTypeExecCmd     = "exec_cmd"
	MsgTypePing        = "ping"

	// File operations (Server → Agent)
	MsgTypeListFiles      = "list_files"
	MsgTypeDownloadFile   = "download_file"
	MsgTypeUploadFile     = "upload_file"
	MsgTypeCreateFile     = "create_file"
	MsgTypeCreateFolder   = "create_folder"
	MsgTypeDeleteItem     = "delete_item"
	MsgTypeCopyItem       = "copy_item"
	MsgTypeMoveItem       = "move_item"
	MsgTypeRenameItem     = "rename_item"
	MsgTypeStreamFileInfo = "stream_file_info"  // Get file metadata for streaming
	MsgTypeStreamChunk    = "stream_chunk"      // Request file chunk

	// Streaming responses (Agent → Server)
	MsgTypeStreamFileInfoResponse = "stream_file_info_response"
	MsgTypeStreamChunkResponse    = "stream_chunk_response"
)

// Message is the generic wrapper for all JSON messages
type Message struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// --- Agent → Server Messages ---

// RegisterData is sent when agent connects to server
type RegisterData struct {
	DeviceID  string `json:"deviceId"`
	Token     string `json:"token"`
	Hostname  string `json:"hostname"`
	Platform  string `json:"platform"`
	OS        string `json:"os,omitempty"`
	Arch      string `json:"arch,omitempty"`
	GoVersion string `json:"goVersion,omitempty"`
}

// HeartbeatData is sent periodically to keep connection alive
type HeartbeatData struct {
	Uptime int64 `json:"uptime"` // seconds since agent started
}

// PtyDataMsg is sent when terminal has output to send
type PtyDataMsg struct {
	SessionID string `json:"sessionId"`
	Data      string `json:"data"` // base64 encoded
}

// PtyExitMsg is sent when terminal session exits
type PtyExitMsg struct {
	SessionID string `json:"sessionId"`
	Code      int    `json:"code"`
}

// CmdResultData is sent with command execution results
type CmdResultData struct {
	Token    string `json:"token"`
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"` // base64 encoded
	Stderr   string `json:"stderr"` // base64 encoded
}

// CmdErrorData is sent when command execution fails
type CmdErrorData struct {
	Token   string `json:"token"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- Server → Agent Messages ---

// RegisterAckData is the server response to registration
type RegisterAckData struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// SpawnPtyData requests the agent to create a new PTY session
type SpawnPtyData struct {
	SessionID string `json:"sessionId"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
	Username  string `json:"username,omitempty"`
}

// PtyInputData contains input data for a PTY session
type PtyInputData struct {
	SessionID string `json:"sessionId"`
	Data      string `json:"data"` // base64 encoded
}

// PtyResizeData requests terminal resize
type PtyResizeData struct {
	SessionID string `json:"sessionId"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// ClosePtyData requests closing a PTY session
type ClosePtyData struct {
	SessionID string `json:"sessionId"`
}

// ExecCmdData requests command execution
type ExecCmdData struct {
	Token    string   `json:"token"`
	Username string   `json:"username,omitempty"`
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
}

// --- Helper functions ---

// NewMessage creates a new message with the given type and data
func NewMessage(msgType string, data interface{}) (*Message, error) {
	var rawData json.RawMessage
	var err error

	if data != nil {
		rawData, err = json.Marshal(data)
		if err != nil {
			return nil, err
		}
	}

	return &Message{
		Type: msgType,
		Data: rawData,
	}, nil
}

// MarshalMessage creates a JSON message ready to send
func MarshalMessage(msgType string, data interface{}) ([]byte, error) {
	msg, err := NewMessage(msgType, data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// ParseMessage parses a JSON message
func ParseMessage(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// UnmarshalData extracts typed data from a message
func UnmarshalData[T any](msg *Message) (*T, error) {
	var data T
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// --- File Operation Request Messages (Server → Agent) ---

// ListFilesData requests a directory listing
type ListFilesData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
}

// DownloadFileData requests file contents
type DownloadFileData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
}

// UploadFileData uploads a file
type UploadFileData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	FileName  string `json:"fileName"`
	Content   string `json:"content"` // base64 encoded
}

// CreateFileData creates an empty file
type CreateFileData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	FileName  string `json:"fileName"`
	Content   string `json:"content,omitempty"` // base64 encoded, optional
}

// CreateFolderData creates a new folder
type CreateFolderData struct {
	RequestID  string `json:"requestId"`
	Path       string `json:"path"`
	FolderName string `json:"folderName"`
}

// DeleteItemData deletes a file or folder
type DeleteItemData struct {
	RequestID   string `json:"requestId"`
	Path        string `json:"path"`
	IsDirectory bool   `json:"isDirectory"`
}

// CopyItemData copies a file or folder
type CopyItemData struct {
	RequestID  string `json:"requestId"`
	SourcePath string `json:"sourcePath"`
	TargetDir  string `json:"targetDir"`
}

// MoveItemData moves a file or folder
type MoveItemData struct {
	RequestID  string `json:"requestId"`
	SourcePath string `json:"sourcePath"`
	TargetPath string `json:"targetPath"`
}

// RenameItemData renames a file or folder
type RenameItemData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	NewName   string `json:"newName"`
}

// --- File Operation Response Messages (Agent → Server) ---

// FileItem represents a file or directory entry
type FileItem struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Type        string `json:"type"`         // "file", "directory", "link"
	Size        int64  `json:"size"`
	ModTime     string `json:"modTime"`
	Permissions string `json:"permissions"`
	Owner       string `json:"owner,omitempty"`
	Group       string `json:"group,omitempty"`
	Executable  bool   `json:"executable,omitempty"`
	LinkTarget  string `json:"linkTarget,omitempty"`
}

// FileListData is the response to list_files
type FileListData struct {
	RequestID string     `json:"requestId"`
	Path      string     `json:"path"`
	Files     []FileItem `json:"files"`
}

// FileContentData is the response to download_file
type FileContentData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	FileName  string `json:"fileName"`
	Content   string `json:"content"`  // base64 encoded
	MimeType  string `json:"mimeType"`
	Size      int64  `json:"size"`
}

// FileOpResultData is the response to file modification operations
type FileOpResultData struct {
	RequestID  string `json:"requestId"`
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
	UniqueName string `json:"uniqueName,omitempty"` // for copy with name conflict
}

// FileErrorData is sent when a file operation fails
type FileErrorData struct {
	RequestID string `json:"requestId"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
}

// --- Streaming File Operation Messages ---

// StreamFileInfoData requests file metadata for streaming
type StreamFileInfoData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
}

// StreamFileInfoResponseData returns file metadata
type StreamFileInfoResponseData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	FileName  string `json:"fileName"`
	MimeType  string `json:"mimeType"`
	Size      int64  `json:"size"`
	Error     string `json:"error,omitempty"`
}

// StreamChunkData requests a chunk of a file
type StreamChunkData struct {
	RequestID string `json:"requestId"`
	Path      string `json:"path"`
	Offset    int64  `json:"offset"`
	Length    int64  `json:"length"`
}

// StreamChunkResponseData returns a chunk of file data
type StreamChunkResponseData struct {
	RequestID string `json:"requestId"`
	Offset    int64  `json:"offset"`
	Length    int64  `json:"length"`
	Data      string `json:"data"` // base64 encoded chunk
	Error     string `json:"error,omitempty"`
}
