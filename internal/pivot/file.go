package pivot

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"undertow/internal/mux"
)

const FileDestination = "file.undertow.invalid:0"

type FileMessage struct {
	AgentID   string `json:"agent_id,omitempty"`
	Operation string `json:"operation,omitempty"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size,omitempty"`
	OK        bool   `json:"ok,omitempty"`
	Error     string `json:"error,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

func ReadFileMessage(reader *bufio.Reader) (FileMessage, error) {
	var message FileMessage
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > 4096 {
		return message, errors.New("invalid file transfer header")
	}
	if err := json.Unmarshal(line, &message); err != nil {
		return message, errors.New("invalid file transfer header")
	}
	return message, nil
}

func WriteFileMessage(w io.Writer, message FileMessage) error {
	return json.NewEncoder(w).Encode(message)
}

func fileError(stream *mux.Stream, reader *bufio.Reader, err error) {
	_ = WriteFileMessage(stream, FileMessage{Error: err.Error()})
	_ = stream.CloseWrite()
	if reader != nil {
		_, _ = io.Copy(io.Discard, reader)
	}
}

func validTransferPath(path string) bool {
	return path != "" && len(path) <= 2048 && !strings.ContainsRune(path, 0)
}

// TransferFile streams a file through the connected VPN server to one agent.
// Existing destination files are left untouched.
func TransferFile(parent context.Context, session *mux.Mux, agentID, operation, localPath, remotePath string) (FileMessage, error) {
	var result FileMessage
	if session == nil {
		return result, errors.New("VPN session is not connected")
	}
	if agentID == "" || !validTransferPath(remotePath) || !validTransferPath(localPath) || operation != "upload" && operation != "download" {
		return result, errors.New("use upload LOCAL REMOTE or download REMOTE LOCAL with a selected agent")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Minute)
	defer cancel()
	var source *os.File
	var size int64
	if operation == "upload" {
		var err error
		source, err = os.Open(localPath)
		if err != nil {
			return result, err
		}
		defer source.Close()
		info, err := source.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return result, errors.New("upload source must be a regular file")
		}
		size = info.Size()
	} else if _, err := os.Lstat(localPath); err == nil {
		return result, fmt.Errorf("download destination already exists: %s", localPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	stream, err := session.Open(ctx, FileDestination)
	if err != nil {
		return result, err
	}
	defer stream.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-done:
		}
	}()
	if err := WriteFileMessage(stream, FileMessage{AgentID: agentID, Operation: operation, Path: remotePath, Size: size}); err != nil {
		return result, err
	}
	if operation == "download" {
		_ = stream.CloseWrite()
	}
	reader := bufio.NewReaderSize(stream, 4096)
	ready, err := ReadFileMessage(reader)
	if err != nil {
		return result, err
	}
	if ready.Error != "" {
		return result, errors.New(ready.Error)
	}
	if !ready.OK {
		return result, errors.New("agent did not accept file transfer")
	}
	if operation == "upload" {
		hash := sha256.New()
		if _, err := io.CopyN(io.MultiWriter(stream, hash), source, size); err != nil {
			return result, err
		}
		if _, err := stream.Write(hash.Sum(nil)); err != nil {
			return result, err
		}
		_ = stream.CloseWrite()
		result, err = ReadFileMessage(reader)
		if err != nil {
			return result, err
		}
		if result.Error != "" {
			return result, errors.New(result.Error)
		}
		if !result.OK || result.Size != size || result.SHA256 != hex.EncodeToString(hash.Sum(nil)) {
			return result, errors.New("agent upload integrity check failed")
		}
		return result, nil
	}
	if ready.Size < 0 {
		return result, errors.New("invalid download size")
	}
	parentDir := filepath.Dir(localPath)
	temp, err := os.CreateTemp(parentDir, ".undertow-download-*")
	if err != nil {
		return result, err
	}
	defer os.Remove(temp.Name())
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(temp, hash), reader, ready.Size); err != nil {
		temp.Close()
		return result, err
	}
	expected := make([]byte, sha256.Size)
	if _, err := io.ReadFull(reader, expected); err != nil {
		temp.Close()
		return result, err
	}
	if subtle.ConstantTimeCompare(expected, hash.Sum(nil)) != 1 {
		temp.Close()
		return result, errors.New("download SHA-256 mismatch")
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return result, err
	}
	if err := temp.Close(); err != nil {
		return result, err
	}
	if err := os.Link(temp.Name(), localPath); err != nil {
		return result, err
	}
	return FileMessage{OK: true, Size: ready.Size, SHA256: hex.EncodeToString(expected)}, nil
}

func serveFile(ctx context.Context, stream *mux.Stream) {
	defer stream.Close()
	if err := stream.AcceptOpen(ctx); err != nil {
		return
	}
	reader := bufio.NewReaderSize(stream, 4096)
	request, err := ReadFileMessage(reader)
	if err != nil {
		fileError(stream, reader, err)
		return
	}
	if !validTransferPath(request.Path) {
		fileError(stream, reader, errors.New("invalid agent file path"))
		return
	}
	if request.Operation == "upload" && request.Size >= 0 {
		serveUpload(stream, reader, request)
	} else if request.Operation == "download" {
		serveDownload(stream, request)
	} else {
		fileError(stream, reader, errors.New("invalid file transfer operation"))
	}
}

func serveUpload(stream *mux.Stream, reader *bufio.Reader, request FileMessage) {
	if _, err := os.Lstat(request.Path); err == nil {
		fileError(stream, reader, fmt.Errorf("upload destination already exists: %s", request.Path))
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		fileError(stream, reader, err)
		return
	}
	temp, err := os.CreateTemp(filepath.Dir(request.Path), ".undertow-upload-*")
	if err != nil {
		fileError(stream, reader, err)
		return
	}
	defer os.Remove(temp.Name())
	if err := WriteFileMessage(stream, FileMessage{OK: true}); err != nil {
		temp.Close()
		return
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(temp, hash), reader, request.Size); err != nil {
		temp.Close()
		fileError(stream, reader, err)
		return
	}
	expected := make([]byte, sha256.Size)
	if _, err := io.ReadFull(reader, expected); err != nil || subtle.ConstantTimeCompare(expected, hash.Sum(nil)) != 1 {
		temp.Close()
		fileError(stream, reader, errors.New("upload SHA-256 mismatch or incomplete data"))
		return
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		fileError(stream, reader, err)
		return
	}
	if err := temp.Close(); err != nil {
		fileError(stream, reader, err)
		return
	}
	if err := os.Link(temp.Name(), request.Path); err != nil {
		fileError(stream, reader, err)
		return
	}
	_ = WriteFileMessage(stream, FileMessage{OK: true, Size: request.Size, SHA256: hex.EncodeToString(expected)})
	_ = stream.CloseWrite()
}

func serveDownload(stream *mux.Stream, request FileMessage) {
	file, err := os.Open(request.Path)
	if err != nil {
		fileError(stream, bufio.NewReader(stream), err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		fileError(stream, bufio.NewReader(stream), errors.New("download source must be a regular file"))
		return
	}
	if err := WriteFileMessage(stream, FileMessage{OK: true, Size: info.Size()}); err != nil {
		return
	}
	hash := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(stream, hash), file, info.Size()); err != nil {
		return
	}
	_, _ = stream.Write(hash.Sum(nil))
	_ = stream.CloseWrite()
}
