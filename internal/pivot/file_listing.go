package pivot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const fileListingPageSize = 200
const maxFileListingOffset = 100000

// FileListing is agent-produced metadata. It never contains file contents and
// does not run an OS shell. The operator must explicitly request each page.
type FileListing struct {
	Path    string      `json:"path"`
	Parent  string      `json:"parent"`
	Entries []FileEntry `json:"entries"`
	Offset  int         `json:"offset"`
	HasMore bool        `json:"has_more"`
	At      time.Time   `json:"at"`
}

type FileEntry struct {
	Name     string    `json:"name"`
	IsDir    bool      `json:"is_dir"`
	IsLink   bool      `json:"is_link"`
	Size     int64     `json:"size"`
	Mode     string    `json:"mode"`
	Modified time.Time `json:"modified"`
	Error    string    `json:"error,omitempty"`
}

func listFiles(args []string) (FileListing, error) {
	path := "."
	if len(args) > 0 && args[0] != "" {
		path = args[0]
	}
	offset := 0
	if len(args) > 1 {
		var err error
		offset, err = strconv.Atoi(args[1])
		if err != nil || offset < 0 || offset > maxFileListingOffset {
			return FileListing{}, errors.New("invalid file listing offset")
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return FileListing{}, err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return FileListing{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return FileListing{}, err
	}
	if !info.IsDir() {
		return FileListing{}, fmt.Errorf("not a directory: %s", absolute)
	}
	listing := FileListing{Path: absolute, Parent: filepath.Dir(absolute), Entries: make([]FileEntry, 0, fileListingPageSize), Offset: offset, At: time.Now().UTC()}
	// Read only the requested page. This bounds agent memory and response size
	// even when the directory contains very many entries.
	for skipped := 0; skipped < offset; {
		batch := offset - skipped
		if batch > fileListingPageSize {
			batch = fileListingPageSize
		}
		items, readErr := file.ReadDir(batch)
		skipped += len(items)
		if readErr == io.EOF || len(items) == 0 {
			return listing, nil
		}
		if readErr != nil {
			return FileListing{}, readErr
		}
	}
	items, readErr := file.ReadDir(fileListingPageSize + 1)
	if readErr != nil && readErr != io.EOF {
		return FileListing{}, readErr
	}
	listing.HasMore = len(items) > fileListingPageSize
	if listing.HasMore {
		items = items[:fileListingPageSize]
	}
	for _, item := range items {
		entry := FileEntry{Name: item.Name(), IsDir: item.IsDir(), IsLink: item.Type()&os.ModeSymlink != 0}
		metadata, infoErr := item.Info()
		if infoErr != nil {
			entry.Error = infoErr.Error()
		} else {
			entry.Size, entry.Mode, entry.Modified = metadata.Size(), metadata.Mode().String(), metadata.ModTime().UTC()
		}
		listing.Entries = append(listing.Entries, entry)
	}
	return listing, nil
}
