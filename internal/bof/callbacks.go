package bof

import (
	"encoding/binary"
	"errors"
)

const (
	CallbackFile      uint32 = 2
	CallbackFileWrite uint32 = 8
	CallbackFileClose uint32 = 9
	CallbackOutput    uint32 = 0
	CallbackError     uint32 = 13
	CallbackChunkSize        = 8<<10 - 5
)

// A callback frame carries the original Beacon type and event boundaries.
// Several frames may represent one BeaconOutput call. bit 0 is first, bit 1 last.
func EncodeCallbackChunk(kind uint32, flags byte, data []byte) []byte {
	frame := make([]byte, 5+len(data))
	binary.BigEndian.PutUint32(frame[:4], kind)
	frame[4] = flags
	copy(frame[5:], data)
	return frame
}

func DecodeCallbackChunk(frame []byte) (uint32, byte, []byte, error) {
	if len(frame) < 5 || frame[4]&^byte(3) != 0 {
		return 0, 0, nil, errors.New("invalid BOF callback frame")
	}
	return binary.BigEndian.Uint32(frame[:4]), frame[4], frame[5:], nil
}
