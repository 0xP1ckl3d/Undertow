package bof

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	WorkerOutput   byte = 'O'
	WorkerStderr   byte = 'D'
	WorkerError    byte = 'E'
	WorkerExit     byte = 'X'
	WorkerCallback byte = 'C'
)

func workerFrame(writer io.Writer, kind byte, data []byte) error {
	if len(data) > 8<<10 {
		return errors.New("BOF worker frame exceeds limit")
	}
	var header [5]byte
	header[0] = kind
	binary.BigEndian.PutUint32(header[1:], uint32(len(data)))
	if _, err := writer.Write(header[:]); err != nil {
		return err
	}
	_, err := writer.Write(data)
	return err
}

// WorkerMain runs in Undertow's short lived child process.
func WorkerMain(input io.Reader, output io.Writer) error {
	var header [8]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return fmt.Errorf("BOF worker header: %w", err)
	}
	objectSize := binary.BigEndian.Uint32(header[:4])
	argumentSize := binary.BigEndian.Uint32(header[4:])
	if objectSize < 20 || objectSize > MaxObjectSize || argumentSize < 4 || argumentSize > MaxArguments+4 {
		return errors.New("invalid BOF worker lengths")
	}
	object := make([]byte, objectSize)
	arguments := make([]byte, argumentSize)
	if _, err := io.ReadFull(input, object); err != nil {
		return err
	}
	if _, err := io.ReadFull(input, arguments); err != nil {
		return err
	}
	var mu sync.Mutex
	write := func(callback uint32, data []byte) error {
		kind := WorkerOutput
		if callback == CallbackError || callback == 1 {
			kind = WorkerStderr
		}
		mu.Lock()
		defer mu.Unlock()
		if callback == CallbackFile || callback == CallbackFileWrite || callback == CallbackFileClose {
			first := true
			for {
				n := len(data)
				if n > CallbackChunkSize {
					n = CallbackChunkSize
				}
				flags := byte(0)
				if first {
					flags |= 1
				}
				if n == len(data) {
					flags |= 2
				}
				if err := workerFrame(output, WorkerCallback, EncodeCallbackChunk(callback, flags, data[:n])); err != nil {
					return err
				}
				data = data[n:]
				first = false
				if len(data) == 0 {
					return nil
				}
			}
		}
		for len(data) > 0 {
			n := len(data)
			if n > 8<<10 {
				n = 8 << 10
			}
			if err := workerFrame(output, kind, data[:n]); err != nil {
				return err
			}
			data = data[n:]
		}
		return nil
	}
	code, err := ExecuteCallbacks(context.Background(), object, arguments, write)
	mu.Lock()
	defer mu.Unlock()
	if err != nil {
		message := []byte(err.Error())
		if len(message) > 1024 {
			message = message[:1024]
		}
		if writeErr := workerFrame(output, WorkerError, message); writeErr != nil {
			return writeErr
		}
	}
	var result [4]byte
	binary.BigEndian.PutUint32(result[:], uint32(int32(code)))
	return workerFrame(output, WorkerExit, result[:])
}
