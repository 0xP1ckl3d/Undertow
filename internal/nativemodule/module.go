package nativemodule

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"unicode/utf8"
)

const MaxSize = 8 << 20
const ABIv1 = "undertow_native_v1"

var magic = []byte("UTN1")

type Metadata struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Runtime     string `json:"runtime"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	ABI         string `json:"abi"`
	Description string `json:"description,omitempty"`
}

func Pack(meta Metadata, dll []byte) ([]byte, error) {
	if err := validateMetadata(meta); err != nil {
		return nil, err
	}
	if err := validatePE(dll, meta.Arch); err != nil {
		return nil, err
	}
	header, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	if len(header) > 4096 || 8+len(header)+len(dll) > MaxSize {
		return nil, errors.New("native module exceeds 8 MiB limit")
	}
	out := make([]byte, 8, 8+len(header)+len(dll))
	copy(out, magic)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(header)))
	out = append(out, header...)
	out = append(out, dll...)
	return out, nil
}

func Parse(source []byte) (Metadata, []byte, error) {
	var meta Metadata
	if len(source) < 9 || len(source) > MaxSize || !bytes.Equal(source[:4], magic) {
		return meta, nil, errors.New("invalid native module container")
	}
	n := int(binary.LittleEndian.Uint32(source[4:8]))
	if n < 2 || n > 4096 || 8+n >= len(source) {
		return meta, nil, errors.New("invalid native module metadata length")
	}
	decoder := json.NewDecoder(bytes.NewReader(source[8 : 8+n]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&meta); err != nil {
		return meta, nil, fmt.Errorf("invalid native module metadata: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return meta, nil, errors.New("invalid trailing native module metadata")
	}
	if err := validateMetadata(meta); err != nil {
		return meta, nil, err
	}
	dll := source[8+n:]
	if err := validatePE(dll, meta.Arch); err != nil {
		return meta, nil, err
	}
	return meta, dll, nil
}

func Compatible(meta Metadata) error {
	if meta.ABI != ABIv1 {
		return fmt.Errorf("unsupported native ABI %q (supported: %s)", meta.ABI, ABIv1)
	}
	if meta.OS != runtime.GOOS {
		return fmt.Errorf("unsupported module OS %q on %s agent", meta.OS, runtime.GOOS)
	}
	if meta.Arch != runtime.GOARCH {
		return fmt.Errorf("unsupported module architecture %q on %s agent", meta.Arch, runtime.GOARCH)
	}
	return nil
}

func validateMetadata(meta Metadata) error {
	if meta.Name == "" || len(meta.Name) > 128 || !utf8.ValidString(meta.Name) || meta.Version == "" || len(meta.Version) > 64 || !utf8.ValidString(meta.Version) || len(meta.Description) > 1024 || !utf8.ValidString(meta.Description) {
		return errors.New("native module name, version or description is invalid")
	}
	if meta.Runtime != "native" {
		return fmt.Errorf("unsupported module runtime %q", meta.Runtime)
	}
	if meta.OS != "windows" {
		return fmt.Errorf("unsupported module OS %q", meta.OS)
	}
	if meta.Arch != "amd64" {
		return fmt.Errorf("unsupported module architecture %q", meta.Arch)
	}
	if meta.ABI == "" || len(meta.ABI) > 64 {
		return errors.New("native module ABI is missing or invalid")
	}
	return nil
}

func validatePE(dll []byte, arch string) error {
	if len(dll) < 0x40 || dll[0] != 'M' || dll[1] != 'Z' {
		return errors.New("invalid native PE module: missing MZ header")
	}
	offset := uint64(binary.LittleEndian.Uint32(dll[0x3c:0x40]))
	if offset > uint64(len(dll)) || uint64(len(dll))-offset < 0x1a {
		return errors.New("invalid native PE module: truncated PE header")
	}
	h := dll[int(offset):]
	if !bytes.Equal(h[:4], []byte{'P', 'E', 0, 0}) {
		return errors.New("invalid native PE module: missing PE signature")
	}
	machine := binary.LittleEndian.Uint16(h[4:6])
	if arch == "amd64" && machine != 0x8664 {
		return fmt.Errorf("unsupported module architecture: PE machine 0x%04x does not match amd64", machine)
	}
	if binary.LittleEndian.Uint16(h[22:24])&0x2000 == 0 {
		return errors.New("native module payload must be a DLL")
	}
	if binary.LittleEndian.Uint16(h[24:26]) != 0x20b {
		return errors.New("native module must be PE32+ (64-bit)")
	}
	if err := validateRelocations(dll, h); err != nil {
		return err
	}
	return nil
}

// V1 accepts the x64 relocation records emitted by the reference MSVC build.
// RVA translation is bounded to the original file, before LoadLibraryEx sees it.
func validateRelocations(dll, h []byte) error {
	sections := int(binary.LittleEndian.Uint16(h[6:8]))
	optionSize := int(binary.LittleEndian.Uint16(h[20:22]))
	if sections < 1 || sections > 96 || optionSize < 160 || 24+optionSize+sections*40 > len(h) {
		return errors.New("invalid native PE section or optional header")
	}
	option := h[24 : 24+optionSize]
	if binary.LittleEndian.Uint32(option[108:112]) <= 5 {
		return errors.New("native DLL has no base relocation directory")
	}
	relocationRVA := binary.LittleEndian.Uint32(option[152:156])
	relocationSize := binary.LittleEndian.Uint32(option[156:160])
	if relocationRVA == 0 || relocationSize < 8 || relocationSize > 1<<20 {
		return errors.New("native DLL has no valid base relocation directory")
	}
	sectionTable := h[24+optionSize:]
	var relocation []byte
	for i := 0; i < sections; i++ {
		section := sectionTable[i*40 : (i+1)*40]
		virtual := binary.LittleEndian.Uint32(section[12:16])
		rawSize := binary.LittleEndian.Uint32(section[16:20])
		rawOffset := binary.LittleEndian.Uint32(section[20:24])
		if relocationRVA < virtual || uint64(relocationRVA-virtual)+uint64(relocationSize) > uint64(rawSize) {
			continue
		}
		start := uint64(rawOffset) + uint64(relocationRVA-virtual)
		if start+uint64(relocationSize) > uint64(len(dll)) {
			return errors.New("invalid native PE relocation bounds")
		}
		relocation = dll[int(start):int(start+uint64(relocationSize))]
		break
	}
	if relocation == nil {
		return errors.New("invalid native PE relocation RVA")
	}
	for len(relocation) > 0 {
		if len(relocation) < 8 {
			return errors.New("truncated native PE relocation block")
		}
		blockSize := int(binary.LittleEndian.Uint32(relocation[4:8]))
		if blockSize < 8 || blockSize > len(relocation) || blockSize&1 != 0 {
			return errors.New("invalid native PE relocation block")
		}
		for offset := 8; offset < blockSize; offset += 2 {
			kind := binary.LittleEndian.Uint16(relocation[offset:offset+2]) >> 12
			if kind != 0 && kind != 10 {
				return fmt.Errorf("unsupported native PE relocation type %d (expected ABSOLUTE or DIR64)", kind)
			}
		}
		relocation = relocation[blockSize:]
	}
	return nil
}

// EncodeArgs is a little-endian argc, followed by length-prefixed UTF-8
// strings, then a length-prefixed opaque data buffer. No NUL terminators occur.
func EncodeArgs(args []string, data []byte) ([]byte, error) {
	if len(args) > 256 || len(data) > 64<<10 {
		return nil, errors.New("native arguments exceed limit")
	}
	size := 8 + len(data)
	for _, arg := range args {
		if !utf8.ValidString(arg) || len(arg) > 64<<10 {
			return nil, errors.New("native argument is not UTF-8 or exceeds 64 KiB")
		}
		size += 4 + len(arg)
	}
	if size > 128<<10 {
		return nil, errors.New("native arguments exceed 128 KiB")
	}
	out := make([]byte, 4, size)
	binary.LittleEndian.PutUint32(out, uint32(len(args)))
	for _, arg := range args {
		var length [4]byte
		binary.LittleEndian.PutUint32(length[:], uint32(len(arg)))
		out = append(out, length[:]...)
		out = append(out, arg...)
	}
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(data)))
	out = append(out, length[:]...)
	return append(out, data...), nil
}
