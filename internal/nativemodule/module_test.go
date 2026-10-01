package nativemodule

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "native", "hello", "hello.module"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestModuleCompatibilityAndMalformedInput(t *testing.T) {
	source := fixture(t)
	meta, dll, err := Parse(source)
	if err != nil || meta.ABI != ABIv1 || meta.Arch != "amd64" || len(dll) < 1024 {
		t.Fatalf("metadata=%+v dll=%d err=%v", meta, len(dll), err)
	}
	if _, _, err := Parse([]byte("bad")); err == nil {
		t.Fatal("malformed module accepted")
	}
	arch := append([]byte(nil), source...)
	header := int(binary.LittleEndian.Uint32(arch[4:8]))
	peStart := 8 + header
	peOffset := int(binary.LittleEndian.Uint32(arch[peStart+0x3c:]))
	binary.LittleEndian.PutUint16(arch[peStart+peOffset+4:], 0xaa64)
	if _, _, err := Parse(arch); err == nil || !strings.Contains(err.Error(), "architecture") {
		t.Fatalf("architecture error=%v", err)
	}
	relocation := append([]byte(nil), source...)
	pe := relocation[peStart+peOffset:]
	optionSize := int(binary.LittleEndian.Uint16(pe[20:22]))
	option := pe[24 : 24+optionSize]
	rva := binary.LittleEndian.Uint32(option[152:156])
	sectionCount := int(binary.LittleEndian.Uint16(pe[6:8]))
	for i := 0; i < sectionCount; i++ {
		section := pe[24+optionSize+i*40:]
		virtual := binary.LittleEndian.Uint32(section[12:16])
		rawSize := binary.LittleEndian.Uint32(section[16:20])
		if rva < virtual || rva-virtual >= rawSize {
			continue
		}
		entry := peStart + int(binary.LittleEndian.Uint32(section[20:24])+rva-virtual) + 8
		value := binary.LittleEndian.Uint16(relocation[entry:])
		binary.LittleEndian.PutUint16(relocation[entry:], value&0x0fff|3<<12)
		break
	}
	if _, _, err := Parse(relocation); err == nil || !strings.Contains(err.Error(), "relocation type") {
		t.Fatalf("relocation error=%v", err)
	}
	abi := Metadata{Name: meta.Name, Version: meta.Version, Runtime: "native", OS: "windows", Arch: "amd64", ABI: "undertow_native_v99"}
	packed, err := Pack(abi, dll)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := Parse(packed)
	if err != nil {
		t.Fatal(err)
	}
	if err := Compatible(parsed); err == nil || !strings.Contains(err.Error(), "ABI") {
		t.Fatalf("ABI error=%v", err)
	}
}

func TestEncodeArgsBinarySafe(t *testing.T) {
	encoded, err := EncodeArgs([]string{"日本語", "a\x00b"}, []byte{0, 255, 1})
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(encoded) != 2 || binary.LittleEndian.Uint32(encoded[len(encoded)-7:]) != 3 || encoded[len(encoded)-2] != 255 {
		t.Fatalf("encoded arguments=%v", encoded)
	}
}
