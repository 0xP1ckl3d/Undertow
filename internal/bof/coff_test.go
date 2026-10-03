package bof

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "examples", name+".o")
	if name == "unsupported_imports" {
		path = filepath.Join("testdata", name+".o")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestParseCorpus(t *testing.T) {
	for _, name := range []string{"hello", "arguments", "imports", "loaderimports"} {
		t.Run(name, func(t *testing.T) {
			c, err := Parse(fixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			if !c.Supported || len(c.Relocations) == 0 || len(c.Sections) < 2 {
				t.Fatalf("compatibility=%+v", c)
			}
		})
	}
}
func TestArgumentBytes(t *testing.T) {
	packed, err := EncodeArguments("iszZb", []string{"123", "-7", "hello", "雪", "base64:AP8="}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 30, 0, 0, 0, 123, 0xff, 0xf9, 0, 0, 0, 6, 'h', 'e', 'l', 'l', 'o', 0, 0, 0, 0, 4, 0xea, 0x96, 0, 0, 0, 0, 0, 2, 0, 0xff}
	if !bytes.Equal(packed, want) {
		t.Fatalf("packed=%v want=%v", packed, want)
	}
	if _, err := EncodeArguments("i", []string{"text"}, nil); err == nil {
		t.Fatal("invalid integer accepted")
	}
	if _, err := EncodeArguments("x", []string{"1"}, nil); err == nil {
		t.Fatal("unknown format accepted")
	}
	if _, err := EncodeArguments("b", []string{"file"}, nil); err == nil {
		t.Fatal("implicit binary format accepted")
	}
}

func TestManifestCompatibilityAndOptionalArguments(t *testing.T) {
	legacy, format, err := ParseManifest([]byte(`{"name":"legacy","arguments":[{"name":"target","type":"string"}]}`))
	if err != nil || format != "z" || !legacy.Arguments[0].IsRequired() {
		t.Fatalf("legacy manifest: %+v %q %v", legacy, format, err)
	}
	modern, format, err := ParseManifest([]byte(`{"description":"example","arguments":[{"name":"target","type":"string","required":true},{"name":"count","type":"int","required":false}]}`))
	if err != nil || format != "zi" || modern.Arguments[1].IsRequired() {
		t.Fatalf("optional manifest: %+v %q %v", modern, format, err)
	}
	if _, _, err := ParseManifest([]byte(`{"arguments":[{"type":"string","required":false},{"type":"int","required":true}]}`)); err == nil {
		t.Fatal("required argument accepted after an optional argument")
	}
}
func TestMalformedCOFF(t *testing.T) {
	base := fixture(t, "hello")
	mutate := func(edit func([]byte), want string) {
		t.Helper()
		b := append([]byte(nil), base...)
		edit(b)
		c, err := Parse(b)
		if err == nil && c.Supported {
			t.Fatalf("accepted malformed object %q", want)
		}
		message := ""
		if err != nil {
			message = err.Error()
		} else {
			message = strings.Join(c.Errors, "; ")
		}
		if !strings.Contains(message, want) {
			t.Fatalf("error=%q want %q", message, want)
		}
	}
	mutate(func(b []byte) { b[0] = 0 }, "architecture")
	mutate(func(b []byte) { binary.LittleEndian.PutUint16(b[2:], 0xffff) }, "section count")
	mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[20+20:], 0xffffffff) }, "offset")
	mutate(func(b []byte) { binary.LittleEndian.PutUint32(b[8:], 0xffffffff) }, "symbol table")
	mutate(func(b []byte) {
		s := binary.LittleEndian.Uint32(b[8:])
		n := binary.LittleEndian.Uint32(b[12:])
		binary.LittleEndian.PutUint32(b[s+n*18:], 0xffffffff)
	}, "string table")
	mutate(func(b []byte) {
		s := firstRelocation(b)
		binary.LittleEndian.PutUint32(b[s+4:], 0xffffffff)
	}, "symbol index")
	mutate(func(b []byte) {
		s := firstRelocation(b)
		binary.LittleEndian.PutUint16(b[s+8:], 0x7777)
	}, "unsupported AMD64 COFF relocation")
	mutate(func(b []byte) {
		s := binary.LittleEndian.Uint32(b[8:])
		n := binary.LittleEndian.Uint32(b[12:])
		for i := uint32(0); i < n; i++ {
			p := s + i*18
			if string(b[p:p+2]) == "go" && b[p+2] == 0 {
				b[p] = 'x'
				break
			}
		}
	}, "missing go")
}

func TestImportCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, kind, want string }{
		{"KERNEL32$GetCurrentProcessId", "windows", "kernel32.dll"},
		{"__imp_ADVAPI32$OpenProcessToken", "windows", "advapi32.dll"},
		{"__imp_NETAPI32$DsGetDcNameA", "windows", "netapi32.dll"},
		{"__imp_KERNEL32$CreateFileW", "windows", "kernel32.dll"},
		{"NETAPI32$NetUserEnum", "windows", "netapi32.dll"},
		{"IPHLPAPI$GetAdaptersAddresses", "windows", "iphlpapi.dll"},
		{"BeaconDataParse", "beacon", "BeaconDataParse"},
		{"__imp_BeaconDataPtr", "beacon", "BeaconDataPtr"},
		{"GetModuleHandleA", "windows", "kernel32.dll"},
		{"__imp_GetModuleHandleA", "windows", "kernel32.dll"},
		{"LoadLibraryA", "windows", "kernel32.dll"},
		{"__imp_LoadLibraryA", "windows", "kernel32.dll"},
		{"GetProcAddress", "windows", "kernel32.dll"},
		{"__imp_GetProcAddress", "windows", "kernel32.dll"},
		{"FreeLibrary", "windows", "kernel32.dll"},
		{"__imp_FreeLibrary", "windows", "kernel32.dll"},
	} {
		imp, kind, err := ClassifyImport(tc.name)
		if err != nil || kind != tc.kind {
			t.Fatalf("%s: %s %v", tc.name, kind, err)
		}
		got := imp.Library
		if kind == "beacon" {
			got = imp.Export
		}
		if !strings.EqualFold(got, tc.want) {
			t.Fatalf("%s: %s", tc.name, got)
		}
		if imp.Indirect != strings.HasPrefix(tc.name, "__imp_") {
			t.Fatalf("%s: indirect=%t", tc.name, imp.Indirect)
		}
	}
	if _, _, err := ClassifyImport("__imp_CreateFileW"); err == nil || !strings.Contains(err.Error(), "unsupported external") {
		t.Fatalf("unknown plain indirect import: %v", err)
	}
	if _, _, err := ClassifyImport("BeaconMissing"); err == nil || !strings.Contains(err.Error(), "unsupported Beacon API") {
		t.Fatalf("unsupported Beacon import: %v", err)
	}
	if _, _, err := ClassifyImport("missing_external"); err == nil || !strings.Contains(err.Error(), "unsupported external") {
		t.Fatalf("unsupported external: %v", err)
	}
}

func TestInspectReportsEveryUnsupportedExternal(t *testing.T) {
	compat, err := Parse(fixture(t, "unsupported_imports"))
	if err != nil {
		t.Fatal(err)
	}
	if compat.Supported {
		t.Fatal("unsupported fixture was accepted")
	}
	for _, symbol := range []string{"__imp_MissingPlainImport", "BeaconUnsupportedHelper", "OtherUnknownExternal"} {
		found := false
		for _, imp := range compat.UnknownImports {
			if imp.Name == symbol {
				found = true
				break
			}
		}
		if !found || !strings.Contains(strings.Join(compat.Errors, "\n"), symbol) {
			t.Fatalf("missing %s in %+v", symbol, compat)
		}
	}
}

func TestParseCorruptionsNeverPanic(t *testing.T) {
	base := fixture(t, "hello")
	for i := 0; i < len(base); i += 7 {
		for _, value := range []byte{0, 0xff} {
			object := append([]byte(nil), base...)
			object[i] = value
			_, _ = Parse(object)
		}
	}
	for i := 0; i < len(base); i += 13 {
		_, _ = Parse(base[:i])
	}
}

func firstRelocation(object []byte) int {
	for i := 0; i < int(binary.LittleEndian.Uint16(object[2:4])); i++ {
		section := object[20+i*40:]
		if binary.LittleEndian.Uint16(section[32:34]) > 0 {
			return int(binary.LittleEndian.Uint32(section[24:28]))
		}
	}
	return 0
}
