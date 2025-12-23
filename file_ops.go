// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

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

// StreamFileInfo returns file metadata for streaming
func (f *FileOps) StreamFileInfo(data *StreamFileInfoData) {
	log.Debug().Str("path", data.Path).Msg("stream file info request")

	info, err := os.Stat(data.Path)
	if err != nil {
		f.sendResult(MsgTypeStreamFileInfoResponse, StreamFileInfoResponseData{
			RequestID: data.RequestID,
			Path:      data.Path,
			Error:     fmt.Sprintf("Failed to stat file: %v", err),
		})
		return
	}

	if info.IsDir() {
		f.sendResult(MsgTypeStreamFileInfoResponse, StreamFileInfoResponseData{
			RequestID: data.RequestID,
			Path:      data.Path,
			Error:     "Cannot stream a directory",
		})
		return
	}

	// Detect MIME type
	mimeType := mime.TypeByExtension(filepath.Ext(data.Path))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	f.sendResult(MsgTypeStreamFileInfoResponse, StreamFileInfoResponseData{
		RequestID: data.RequestID,
		Path:      data.Path,
		FileName:  filepath.Base(data.Path),
		MimeType:  mimeType,
		Size:      info.Size(),
	})
}

// StreamChunk reads and returns a chunk of a file
func (f *FileOps) StreamChunk(data *StreamChunkData) {
	log.Debug().
		Str("path", data.Path).
		Int64("offset", data.Offset).
		Int64("length", data.Length).
		Msg("stream chunk request")

	file, err := os.Open(data.Path)
	if err != nil {
		f.sendResult(MsgTypeStreamChunkResponse, StreamChunkResponseData{
			RequestID: data.RequestID,
			Error:     fmt.Sprintf("Failed to open file: %v", err),
		})
		return
	}
	defer file.Close()

	// Seek to offset
	_, err = file.Seek(data.Offset, io.SeekStart)
	if err != nil {
		f.sendResult(MsgTypeStreamChunkResponse, StreamChunkResponseData{
			RequestID: data.RequestID,
			Error:     fmt.Sprintf("Failed to seek: %v", err),
		})
		return
	}

	// Read chunk
	chunk := make([]byte, data.Length)
	n, err := io.ReadFull(file, chunk)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		f.sendResult(MsgTypeStreamChunkResponse, StreamChunkResponseData{
			RequestID: data.RequestID,
			Error:     fmt.Sprintf("Failed to read: %v", err),
		})
		return
	}

	// Only return actual bytes read
	chunk = chunk[:n]

	f.sendResult(MsgTypeStreamChunkResponse, StreamChunkResponseData{
		RequestID: data.RequestID,
		Offset:    data.Offset,
		Length:    int64(n),
		Data:      base64.StdEncoding.EncodeToString(chunk),
	})
}

// GetDirStats calculates directory statistics (size, file count, folder count)
func (f *FileOps) GetDirStats(data *GetDirStatsData) {
	log.Debug().Str("path", data.Path).Msg("getting directory stats")

	info, err := os.Stat(data.Path)
	if err != nil {
		log.Error().Err(err).Str("path", data.Path).Msg("failed to stat path for dir stats")
		f.sendResult(MsgTypeDirStats, DirStatsData{
			RequestID: data.RequestID,
			Path:      data.Path,
			Error:     fmt.Sprintf("Failed to stat path: %v", err),
		})
		return
	}

	// If it's a file, just return its size
	if !info.IsDir() {
		f.sendResult(MsgTypeDirStats, DirStatsData{
			RequestID:   data.RequestID,
			Path:        data.Path,
			TotalSize:   info.Size(),
			FileCount:   1,
			FolderCount: 0,
		})
		return
	}

	// Walk directory recursively
	var totalSize int64
	var fileCount int64
	var folderCount int64

	err = filepath.Walk(data.Path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip files/dirs we can't access
			return nil
		}

		if info.IsDir() {
			// Don't count the root directory itself
			if path != data.Path {
				folderCount++
			}
		} else {
			fileCount++
			totalSize += info.Size()
		}
		return nil
	})

	if err != nil {
		log.Error().Err(err).Str("path", data.Path).Msg("failed to walk directory for stats")
		f.sendResult(MsgTypeDirStats, DirStatsData{
			RequestID: data.RequestID,
			Path:      data.Path,
			Error:     fmt.Sprintf("Failed to walk directory: %v", err),
		})
		return
	}

	log.Debug().
		Str("path", data.Path).
		Int64("totalSize", totalSize).
		Int64("fileCount", fileCount).
		Int64("folderCount", folderCount).
		Msg("directory stats calculated")

	f.sendResult(MsgTypeDirStats, DirStatsData{
		RequestID:   data.RequestID,
		Path:        data.Path,
		TotalSize:   totalSize,
		FileCount:   fileCount,
		FolderCount: folderCount,
	})
}

// CompressFiles compresses files into an archive
func (f *FileOps) CompressFiles(data *CompressFilesData) {
	log.Debug().
		Strs("paths", data.Paths).
		Str("archiveName", data.ArchiveName).
		Str("format", data.Format).
		Msg("compressing files")

	if len(data.Paths) == 0 {
		log.Warn().Msg("compress request with no files")
		f.sendError(data.RequestID, 400, "No files to compress")
		return
	}

	// Determine working directory from first path
	firstPath := data.Paths[0]
	workingDir := filepath.Dir(firstPath)

	// Get base names for files
	var fileNames []string
	for _, p := range data.Paths {
		fileNames = append(fileNames, filepath.Base(p))
	}

	// Determine archive path
	archivePath := data.ArchiveName
	if !strings.Contains(archivePath, "/") {
		archivePath = filepath.Join(workingDir, archivePath)
	}

	// Build compression command based on format
	var cmd *exec.Cmd
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	format := data.Format
	if format == "" {
		format = "zip"
	}

	switch format {
	case "zip":
		args := append([]string{"-r", archivePath}, fileNames...)
		cmd = exec.CommandContext(ctx, "zip", args...)
	case "tar.gz", "tgz":
		args := append([]string{"-czf", archivePath}, fileNames...)
		cmd = exec.CommandContext(ctx, "tar", args...)
	case "tar.bz2", "tbz2":
		args := append([]string{"-cjf", archivePath}, fileNames...)
		cmd = exec.CommandContext(ctx, "tar", args...)
	case "tar.xz":
		args := append([]string{"-cJf", archivePath}, fileNames...)
		cmd = exec.CommandContext(ctx, "tar", args...)
	case "tar":
		args := append([]string{"-cf", archivePath}, fileNames...)
		cmd = exec.CommandContext(ctx, "tar", args...)
	case "7z":
		args := append([]string{"a", archivePath}, fileNames...)
		cmd = exec.CommandContext(ctx, "7z", args...)
	default:
		log.Warn().Str("format", format).Msg("unsupported compression format")
		f.sendError(data.RequestID, 400, fmt.Sprintf("Unsupported compression format: %s", format))
		return
	}

	cmd.Dir = workingDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		log.Error().
			Err(err).
			Str("archivePath", archivePath).
			Str("format", format).
			Str("stderr", errMsg).
			Msg("compression failed")
		f.sendError(data.RequestID, 500, fmt.Sprintf("Compression failed: %s", errMsg))
		return
	}

	log.Debug().
		Str("archivePath", archivePath).
		Str("format", format).
		Int("fileCount", len(data.Paths)).
		Msg("compression completed successfully")

	f.sendOpResult(data.RequestID, true, fmt.Sprintf("Created %s", archivePath), "")
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
