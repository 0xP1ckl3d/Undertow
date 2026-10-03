package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func runPayloadDownload(ctx context.Context, out io.Writer, call consoleCaller, ref, outputPath string) error {
	a, err := resolvePayload(ctx, call, ref)
	if err != nil {
		return err
	}
	if a.Filename == "" || filepath.Base(a.Filename) != a.Filename || a.Filename == "." || a.Filename == ".." {
		return errors.New("payload has an invalid filename")
	}
	if outputPath == "" {
		outputPath = filepath.Join("outputs", "downloads", "payloads", a.Filename)
	} else if stat, err := os.Stat(outputPath); err == nil && stat.IsDir() || strings.HasSuffix(outputPath, "/") || strings.HasSuffix(outputPath, `\`) {
		outputPath = filepath.Join(outputPath, a.Filename)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	destination, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("payload file already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := prepareClientOutputDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".undertow-payload-*.partial")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()

	fmt.Fprintf(out, "Downloading payload %s (%d bytes) to %s...\n", a.ID, a.Size, destination)
	hasher := sha256.New()
	var offset int64
	var nextProgress int64 = 8 << 20
	for offset < a.Size {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := call(ctx, http.MethodGet, "/v1/agent-artifacts/"+url.PathEscape(a.ID)+"/download/chunk?offset="+strconv.FormatInt(offset, 10), nil)
		if err != nil {
			return fmt.Errorf("could not download payload %s: %w", a.ID, err)
		}
		var chunk payloadDownloadChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		if chunk.Offset != offset || chunk.Total != a.Size || len(chunk.Data) == 0 || len(chunk.Data) > payloadDownloadChunkSize || int64(len(chunk.Data)) > a.Size-offset {
			return errors.New("invalid or incomplete payload download chunk")
		}
		if _, err := io.MultiWriter(temporary, hasher).Write(chunk.Data); err != nil {
			return err
		}
		offset += int64(len(chunk.Data))
		if offset >= nextProgress && offset < a.Size {
			fmt.Fprintf(out, "Downloaded %d / %d MiB\n", offset>>20, a.Size>>20)
			nextProgress += 8 << 20
		}
	}
	if a.Size <= 0 || !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), a.SHA256) {
		return errors.New("payload SHA-256 mismatch")
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if a.Platform == "linux" {
		if err := temporary.Chmod(0700); err != nil {
			return err
		}
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := publishJobDownload(temporary.Name(), destination); err != nil {
		return err
	}
	if err := setLocalOutputOwner(destination); err != nil {
		_ = os.Remove(destination)
		return err
	}
	fmt.Fprintf(out, "Saved payload to %s (%d bytes, SHA-256 %s)\n", destination, offset, a.SHA256)
	return nil
}
