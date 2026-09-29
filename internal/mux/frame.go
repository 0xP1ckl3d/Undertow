package mux

import (
	"encoding/binary"
	"errors"
)

const (
	frameOpen byte = 1 + iota
	frameOpenOK
	frameOpenFail
	frameData
	frameFin
	frameReset
	frameWindow
	framePing
	framePong
	frameControl
)

const frameHeader = 20
const maxFrameData = 700

var errFrame = errors.New("invalid multiplex frame")

type frame struct {
	kind   byte
	id     uint64
	offset uint64
	data   []byte
}

func (f frame) encode() []byte {
	b := make([]byte, frameHeader+len(f.data))
	b[0] = 1
	b[1] = f.kind
	binary.BigEndian.PutUint64(b[2:10], f.id)
	binary.BigEndian.PutUint64(b[10:18], f.offset)
	binary.BigEndian.PutUint16(b[18:20], uint16(len(f.data)))
	copy(b[20:], f.data)
	return b
}

func decodeFrame(b []byte) (frame, error) {
	var f frame
	if len(b) < frameHeader || b[0] != 1 || int(binary.BigEndian.Uint16(b[18:20])) != len(b)-frameHeader || len(b)-frameHeader > maxFrameData {
		return f, errFrame
	}
	f.kind = b[1]
	f.id = binary.BigEndian.Uint64(b[2:10])
	f.offset = binary.BigEndian.Uint64(b[10:18])
	f.data = b[20:]
	if f.id == 0 && f.kind != framePing && f.kind != framePong && f.kind != frameControl {
		return frame{}, errFrame
	}
	switch f.kind {
	case frameOpen, frameOpenOK, frameOpenFail, frameData, frameFin, frameReset, frameWindow, framePing, framePong, frameControl:
	default:
		return frame{}, errFrame
	}
	return f, nil
}
