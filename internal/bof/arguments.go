package bof

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const MaxArguments = 1 << 20

type ArgumentSpec struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type Manifest struct {
	Name       string         `json:"name"`
	Entrypoint string         `json:"entrypoint"`
	Arguments  []ArgumentSpec `json:"arguments"`
}

func ParseManifest(src []byte) (Manifest, string, error) {
	var m Manifest
	if len(src) > 16<<10 {
		return m, "", errors.New("BOF manifest exceeds 16 KiB")
	}
	if err := json.Unmarshal(src, &m); err != nil {
		return m, "", fmt.Errorf("invalid BOF manifest: %w", err)
	}
	if m.Entrypoint != "" && m.Entrypoint != "go" {
		return m, "", errors.New("BOF manifest entrypoint must be go")
	}
	var format strings.Builder
	for _, a := range m.Arguments {
		switch a.Type {
		case "int", "int32":
			format.WriteByte('i')
		case "short", "int16":
			format.WriteByte('s')
		case "string", "ansi":
			format.WriteByte('z')
		case "wstring", "wide":
			format.WriteByte('Z')
		case "binary":
			format.WriteByte('b')
		default:
			return m, "", fmt.Errorf("unsupported BOF manifest argument type %q", a.Type)
		}
	}
	return m, format.String(), nil
}

func EncodeArguments(format string, values []string, readBinary func(string) ([]byte, error)) ([]byte, error) {
	if len(format) != len(values) || len(format) > 128 {
		return nil, errors.New("BOF argument count does not match --format")
	}
	payload := make([]byte, 0, 128)
	for i := range format {
		var data []byte
		switch format[i] {
		case 'i':
			n, err := strconv.ParseInt(values[i], 0, 32)
			if err != nil {
				return nil, fmt.Errorf("BOF int32 argument %d: %w", i+1, err)
			}
			data = make([]byte, 4)
			binary.BigEndian.PutUint32(data, uint32(int32(n)))
		case 's':
			n, err := strconv.ParseInt(values[i], 0, 16)
			if err != nil {
				return nil, fmt.Errorf("BOF int16 argument %d: %w", i+1, err)
			}
			data = make([]byte, 2)
			binary.BigEndian.PutUint16(data, uint16(int16(n)))
		case 'z':
			if !utf8.ValidString(values[i]) || strings.ContainsRune(values[i], 0) {
				return nil, fmt.Errorf("BOF ANSI argument %d is invalid", i+1)
			}
			data = append([]byte(values[i]), 0)
		case 'Z':
			if !utf8.ValidString(values[i]) || strings.ContainsRune(values[i], 0) {
				return nil, fmt.Errorf("BOF wide argument %d is invalid", i+1)
			}
			for _, unit := range utf16.Encode([]rune(values[i])) {
				var part [2]byte
				binary.LittleEndian.PutUint16(part[:], unit)
				data = append(data, part[:]...)
			}
			data = append(data, 0, 0)
		case 'b':
			value := values[i]
			if strings.HasPrefix(value, "base64:") {
				var err error
				data, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "base64:"))
				if err != nil {
					return nil, fmt.Errorf("BOF binary argument %d: %w", i+1, err)
				}
			} else if strings.HasPrefix(value, "@") {
				if readBinary == nil {
					return nil, errors.New("BOF binary file resolver unavailable")
				}
				var err error
				data, err = readBinary(value[1:])
				if err != nil {
					return nil, fmt.Errorf("BOF binary argument %d: %w", i+1, err)
				}
			} else {
				return nil, fmt.Errorf("BOF binary argument %d needs @FILE or base64:DATA", i+1)
			}
		default:
			return nil, fmt.Errorf("invalid BOF argument format %q at position %d", format[i], i+1)
		}
		if len(data) > MaxArguments-len(payload)-4 {
			return nil, errors.New("BOF arguments exceed 1 MiB")
		}
		if format[i] == 'i' || format[i] == 's' {
			payload = append(payload, data...)
		} else {
			var size [4]byte
			binary.BigEndian.PutUint32(size[:], uint32(len(data)))
			payload = append(payload, size[:]...)
			payload = append(payload, data...)
		}
	}
	result := make([]byte, 4, 4+len(payload))
	binary.BigEndian.PutUint32(result, uint32(len(payload)))
	return append(result, payload...), nil
}

func ReadBinaryFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxArguments {
		return nil, errors.New("BOF binary argument exceeds 1 MiB")
	}
	return os.ReadFile(path)
}
