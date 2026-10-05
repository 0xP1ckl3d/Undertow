package bof

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxCallbackFile = 512 << 20

type FileArtifact struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Size uint64 `json:"size"`
	Path string `json:"-"`
}

type pendingFile struct {
	artifact FileArtifact
	file     *os.File
	path     string
	declared uint64
}

// FileCollector consumes typed BOF callback frames and stores completed files
// at the operator or server. It never runs on the agent.
type FileCollector struct {
	Root      string
	MaxBytes  uint64
	OnReserve func(uint64) error
	OnRelease func(uint64)
	used      uint64
	files     map[uint32]*pendingFile
	active    bool
	kind      uint32
	header    []byte
	writeID   uint32
}

func NewFileCollector(root string) *FileCollector {
	return &FileCollector{Root: root, MaxBytes: MaxCallbackFile, files: make(map[uint32]*pendingFile)}
}

func safeFileName(name string) string {
	name = strings.TrimRight(name, "\x00")
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.ToValidUTF8(name, "_")
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, " .")
	if len(name) > 128 {
		name = strings.ToValidUTF8(name[:128], "_")
	}
	if name == "." || name == ".." || name == "" {
		return "download.bin"
	}
	return name
}

func (c *FileCollector) Consume(frame []byte) ([]FileArtifact, error) {
	kind, flags, data, err := DecodeCallbackChunk(frame)
	if err != nil {
		return nil, err
	}
	if kind != CallbackFile && kind != CallbackFileWrite && kind != CallbackFileClose {
		return nil, fmt.Errorf("unsupported BOF callback type %d", kind)
	}
	if flags&1 != 0 {
		if c.active {
			return nil, errors.New("interleaved BOF file callbacks")
		}
		c.active, c.kind, c.header, c.writeID = true, kind, nil, 0
	} else if !c.active || c.kind != kind {
		return nil, errors.New("orphan BOF file callback chunk")
	}
	switch kind {
	case CallbackFile:
		if len(c.header)+len(data) > 4096 {
			return nil, errors.New("BOF file metadata exceeds limit")
		}
		c.header = append(c.header, data...)
	case CallbackFileClose:
		if len(c.header)+len(data) > 4 {
			return nil, errors.New("invalid BOF file close callback")
		}
		c.header = append(c.header, data...)
	case CallbackFileWrite:
		if len(c.header) < 4 {
			n := 4 - len(c.header)
			if n > len(data) {
				n = len(data)
			}
			c.header = append(c.header, data[:n]...)
			data = data[n:]
			if len(c.header) == 4 {
				c.writeID = binary.BigEndian.Uint32(c.header)
			}
		}
		if len(data) != 0 {
			file := c.files[c.writeID]
			if file == nil {
				return nil, fmt.Errorf("BOF file write for unknown ID %d", c.writeID)
			}
			if file.artifact.Size+uint64(len(data)) > file.declared {
				return nil, fmt.Errorf("BOF file %d exceeds announced size", c.writeID)
			}
			if uint64(len(data)) > c.MaxBytes-c.used {
				return nil, errors.New("BOF file storage limit reached")
			}
			n, err := file.file.Write(data)
			file.artifact.Size += uint64(n)
			c.used += uint64(n)
			if err != nil {
				return nil, err
			}
			if n != len(data) {
				return nil, io.ErrShortWrite
			}
		}
	}
	if flags&2 == 0 {
		return nil, nil
	}
	c.active = false
	if len(c.header) < 4 {
		return nil, errors.New("truncated BOF file callback")
	}
	id := binary.BigEndian.Uint32(c.header[:4])
	switch kind {
	case CallbackFile:
		if len(c.header) < 9 {
			return nil, errors.New("truncated BOF file metadata")
		}
		if c.files[id] != nil {
			return nil, fmt.Errorf("duplicate BOF file ID %d", id)
		}
		declared := binary.BigEndian.Uint32(c.header[4:8])
		if declared > MaxCallbackFile || uint64(declared) > c.MaxBytes-c.used {
			return nil, errors.New("BOF file storage limit reached")
		}
		if c.OnReserve != nil {
			if err := c.OnReserve(uint64(declared)); err != nil {
				return nil, err
			}
		}
		if err := os.MkdirAll(c.Root, 0700); err != nil {
			if c.OnRelease != nil {
				c.OnRelease(uint64(declared))
			}
			return nil, err
		}
		file, err := os.CreateTemp(c.Root, ".undertow-file-*.partial")
		if err != nil {
			if c.OnRelease != nil {
				c.OnRelease(uint64(declared))
			}
			return nil, err
		}
		c.files[id] = &pendingFile{artifact: FileArtifact{ID: id, Name: safeFileName(string(c.header[8:])), Size: 0}, file: file, path: file.Name(), declared: uint64(declared)}
	case CallbackFileWrite:
		if c.files[id] == nil {
			return nil, fmt.Errorf("BOF file write for unknown ID %d", id)
		}
	case CallbackFileClose:
		if len(c.header) != 4 {
			return nil, errors.New("invalid BOF file close callback")
		}
		file := c.files[id]
		if file == nil {
			return nil, fmt.Errorf("BOF file close for unknown ID %d", id)
		}
		if file.artifact.Size != file.declared {
			return nil, fmt.Errorf("BOF file %d size mismatch: received %d of %d bytes", id, file.artifact.Size, file.declared)
		}
		if err := file.file.Sync(); err != nil {
			return nil, err
		}
		if err := file.file.Close(); err != nil {
			return nil, err
		}
		file.file = nil
		final := filepath.Join(c.Root, fmt.Sprintf("%08x-%s", id, file.artifact.Name))
		if _, err := os.Stat(final); err == nil {
			return nil, fmt.Errorf("BOF file already exists: %s", final)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := os.Rename(file.path, final); err != nil {
			return nil, err
		}
		file.artifact.Path = final
		delete(c.files, id)
		return []FileArtifact{file.artifact}, nil
	}
	return nil, nil
}

func (c *FileCollector) Close() {
	for id, file := range c.files {
		if file.file != nil {
			_ = file.file.Close()
		}
		_ = os.Remove(file.path)
		if c.OnRelease != nil {
			c.OnRelease(file.declared)
		}
		delete(c.files, id)
	}
}
