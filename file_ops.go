// SPDX-License-Identifier: MIT

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/rs/zerolog/log"
)

// FileOps handles file operations for the agent
type FileOps struct {
	sendResult func(msgType string, data interface{})
}

// NewFileOps creates a new FileOps handler
func NewFileOps(sendResult func(msgType string, data interface{})) *FileOps {
	return &FileOps{
		sendResult: sendResult,
	}
}

// sendError sends a file error response
func (f *FileOps) sendError(requestID string, code int, message string) {
	f.sendResult(MsgTypeFileError, FileErrorData{
		RequestID: requestID,
		Code:      code,
		Message:   message,
	})
}

// sendOpResult sends a file operation result
func (f *FileOps) sendOpResult(requestID string, success bool, message string, uniqueName string) {
	f.sendResult(MsgTypeFileOpResult, FileOpResultData{
		RequestID:  requestID,
		Success:    success,
		Message:    message,
		UniqueName: uniqueName,
	})
}

// ListFiles lists the contents of a directory
func (f *FileOps) ListFiles(data *ListFilesData) {
	log.Debug().Str("path", data.Path).Msg("listing files")

	path := data.Path
	if path == "" {
		path = "/"
	}

	// Expand home directory
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(homeDir, path[1:])
		}
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to read directory: %v", err))
		return
	}

	files := make([]FileItem, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		fullPath := filepath.Join(path, entry.Name())
		item := f.fileInfoToItem(fullPath, info)
		files = append(files, item)
	}

	f.sendResult(MsgTypeFileList, FileListData{
		RequestID: data.RequestID,
		Path:      path,
		Files:     files,
	})
}

// DownloadFile reads a file and sends its contents
func (f *FileOps) DownloadFile(data *DownloadFileData) {
	log.Debug().Str("path", data.Path).Msg("downloading file")

	content, err := os.ReadFile(data.Path)
	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to read file: %v", err))
		return
	}

	// Get file info for size
	info, err := os.Stat(data.Path)
	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to stat file: %v", err))
		return
	}

	// Detect MIME type
	mimeType := mime.TypeByExtension(filepath.Ext(data.Path))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	f.sendResult(MsgTypeFileContent, FileContentData{
		RequestID: data.RequestID,
		Path:      data.Path,
		FileName:  filepath.Base(data.Path),
		Content:   base64.StdEncoding.EncodeToString(content),
		MimeType:  mimeType,
		Size:      info.Size(),
	})
}

// UploadFile writes content to a file
func (f *FileOps) UploadFile(data *UploadFileData) {
	log.Debug().Str("path", data.Path).Str("fileName", data.FileName).Msg("uploading file")

	content, err := base64.StdEncoding.DecodeString(data.Content)
	if err != nil {
		f.sendError(data.RequestID, 400, fmt.Sprintf("Invalid base64 content: %v", err))
		return
	}

	fullPath := filepath.Join(data.Path, data.FileName)

	err = os.WriteFile(fullPath, content, 0644)
	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to write file: %v", err))
		return
	}

	f.sendOpResult(data.RequestID, true, "File uploaded successfully", "")
}

// CreateFile creates an empty file or with optional content
func (f *FileOps) CreateFile(data *CreateFileData) {
	log.Debug().Str("path", data.Path).Str("fileName", data.FileName).Msg("creating file")

	fullPath := filepath.Join(data.Path, data.FileName)

	var content []byte
	if data.Content != "" {
		var err error
		content, err = base64.StdEncoding.DecodeString(data.Content)
		if err != nil {
			f.sendError(data.RequestID, 400, fmt.Sprintf("Invalid base64 content: %v", err))
			return
		}
	}

	err := os.WriteFile(fullPath, content, 0644)
	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to create file: %v", err))
		return
	}

	f.sendOpResult(data.RequestID, true, "File created successfully", "")
}

// CreateFolder creates a new directory
func (f *FileOps) CreateFolder(data *CreateFolderData) {
	log.Debug().Str("path", data.Path).Str("folderName", data.FolderName).Msg("creating folder")

	fullPath := filepath.Join(data.Path, data.FolderName)

	err := os.MkdirAll(fullPath, 0755)
	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to create folder: %v", err))
		return
	}

	f.sendOpResult(data.RequestID, true, "Folder created successfully", "")
}

// DeleteItem deletes a file or directory
func (f *FileOps) DeleteItem(data *DeleteItemData) {
	log.Debug().Str("path", data.Path).Bool("isDirectory", data.IsDirectory).Msg("deleting item")

	var err error
	if data.IsDirectory {
		err = os.RemoveAll(data.Path)
	} else {
		err = os.Remove(data.Path)
	}

	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to delete: %v", err))
		return
	}

	f.sendOpResult(data.RequestID, true, "Deleted successfully", "")
}

// CopyItem copies a file or directory
func (f *FileOps) CopyItem(data *CopyItemData) {
	log.Debug().Str("source", data.SourcePath).Str("target", data.TargetDir).Msg("copying item")

	srcInfo, err := os.Stat(data.SourcePath)
	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to stat source: %v", err))
		return
	}

	baseName := filepath.Base(data.SourcePath)
	targetPath := filepath.Join(data.TargetDir, baseName)

	// Handle name conflicts
	uniqueName := baseName
	counter := 1
	for {
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(baseName)
		nameWithoutExt := strings.TrimSuffix(baseName, ext)
		uniqueName = fmt.Sprintf("%s (%d)%s", nameWithoutExt, counter, ext)
		targetPath = filepath.Join(data.TargetDir, uniqueName)
		counter++
	}

	if srcInfo.IsDir() {
		err = copyDir(data.SourcePath, targetPath)
	} else {
		err = copyFile(data.SourcePath, targetPath)
	}

	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to copy: %v", err))
		return
	}

	f.sendOpResult(data.RequestID, true, "Copied successfully", uniqueName)
}

// MoveItem moves a file or directory
func (f *FileOps) MoveItem(data *MoveItemData) {
	log.Debug().Str("source", data.SourcePath).Str("target", data.TargetPath).Msg("moving item")

	err := os.Rename(data.SourcePath, data.TargetPath)
	if err != nil {
		// If rename fails (cross-device), try copy + delete
		srcInfo, statErr := os.Stat(data.SourcePath)
		if statErr != nil {
			f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to move: %v", err))
			return
		}

		if srcInfo.IsDir() {
			err = copyDir(data.SourcePath, data.TargetPath)
		} else {
			err = copyFile(data.SourcePath, data.TargetPath)
		}

		if err != nil {
			f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to move (copy phase): %v", err))
			return
		}

		if srcInfo.IsDir() {
			err = os.RemoveAll(data.SourcePath)
		} else {
			err = os.Remove(data.SourcePath)
		}

		if err != nil {
			log.Warn().Err(err).Msg("Failed to remove source after copy")
		}
	}

	f.sendOpResult(data.RequestID, true, "Moved successfully", "")
}

// RenameItem renames a file or directory
func (f *FileOps) RenameItem(data *RenameItemData) {
	log.Debug().Str("path", data.Path).Str("newName", data.NewName).Msg("renaming item")

	dir := filepath.Dir(data.Path)
	newPath := filepath.Join(dir, data.NewName)

	err := os.Rename(data.Path, newPath)
	if err != nil {
		f.sendError(data.RequestID, 500, fmt.Sprintf("Failed to rename: %v", err))
		return
	}

	f.sendOpResult(data.RequestID, true, "Renamed successfully", "")
}

// fileInfoToItem converts os.FileInfo to FileItem
func (f *FileOps) fileInfoToItem(path string, info fs.FileInfo) FileItem {
	item := FileItem{
		Name:        info.Name(),
		Path:        path,
		Size:        info.Size(),
		ModTime:     info.ModTime().Format("2006-01-02T15:04:05Z07:00"),
		Permissions: info.Mode().Perm().String(),
	}

	// Determine type
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		item.Type = "link"
		// Try to resolve symlink target
		if target, err := os.Readlink(path); err == nil {
			item.LinkTarget = target
		}
	} else if info.IsDir() {
		item.Type = "directory"
	} else {
		item.Type = "file"
		// Check if executable
		if mode&0111 != 0 {
			item.Executable = true
		}
	}

	// Get owner/group on Unix
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		item.Owner = strconv.FormatUint(uint64(stat.Uid), 10)
		item.Group = strconv.FormatUint(uint64(stat.Gid), 10)

		// Try to resolve to names
		if u, err := user.LookupId(item.Owner); err == nil {
			item.Owner = u.Username
		}
		if g, err := user.LookupGroupId(item.Group); err == nil {
			item.Group = g.Name
		}
	}

	return item
}

// Helper functions

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	srcInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}

	destFile, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}
