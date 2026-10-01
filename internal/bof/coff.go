package bof

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const MaxObjectSize = 8 << 20
const MaxRelocations = 100000

type Section struct {
	Name            string
	Data            []byte
	Size            uint32
	Characteristics uint32
	Relocations     []Relocation
}
type Symbol struct {
	Name         string
	Section      int16
	Value        uint32
	StorageClass byte
	Auxiliary    bool
}
type Relocation struct {
	Section     int
	Offset      uint32
	SymbolIndex uint32
	Symbol      string
	Type        uint16
}
type Import struct {
	Name     string
	Library  string
	Export   string
	Indirect bool
}
type Compatibility struct {
	Architecture   string
	Entrypoint     string
	Sections       []Section
	Symbols        []Symbol
	WindowsImports []Import
	BeaconImports  []Import
	UnknownImports []Import
	Relocations    []Relocation
	Supported      bool
	Warnings       []string
	Errors         []string
}

var beaconNames = map[string]bool{
	"BeaconPrintf": true, "BeaconOutput": true,
	"BeaconDataParse": true, "BeaconDataInt": true, "BeaconDataShort": true, "BeaconDataExtract": true, "BeaconDataLength": true,
	"BeaconFormatAlloc": true, "BeaconFormatReset": true, "BeaconFormatFree": true, "BeaconFormatAppend": true,
	"BeaconFormatPrintf": true, "BeaconFormatToString": true, "BeaconFormatInt": true,
}

func SupportedBeacon(name string) bool { return beaconNames[name] }
func SupportedRelocation(kind uint16) bool {
	return kind == 1 || kind == 2 || kind == 3 || kind >= 4 && kind <= 9 || kind == 0xA || kind == 0xB
}
func RelocationName(kind uint16) string {
	names := map[uint16]string{1: "ADDR64", 2: "ADDR32", 3: "ADDR32NB", 4: "REL32", 5: "REL32_1", 6: "REL32_2", 7: "REL32_3", 8: "REL32_4", 9: "REL32_5", 10: "SECTION", 11: "SECREL"}
	if name, ok := names[kind]; ok {
		return "IMAGE_REL_AMD64_" + name
	}
	return fmt.Sprintf("IMAGE_REL_AMD64_0x%04X", kind)
}

func span(src []byte, offset, size uint64) ([]byte, error) {
	if offset > uint64(len(src)) || size > uint64(len(src))-offset {
		return nil, errors.New("COFF offset or size exceeds object")
	}
	return src[int(offset):int(offset+size)], nil
}
func nameFrom(raw []byte, table []byte) (string, error) {
	if len(raw) != 8 {
		return "", errors.New("invalid COFF name")
	}
	if binary.LittleEndian.Uint32(raw[:4]) == 0 && binary.LittleEndian.Uint32(raw[4:]) != 0 {
		offset := binary.LittleEndian.Uint32(raw[4:])
		if offset < 4 || int(offset) >= len(table) {
			return "", errors.New("invalid COFF string-table offset")
		}
		end := offset
		for int(end) < len(table) && table[end] != 0 {
			end++
		}
		if int(end) == len(table) {
			return "", errors.New("unterminated COFF string-table name")
		}
		return string(table[offset:end]), nil
	}
	if raw[0] == '/' {
		text := strings.TrimRight(string(raw[1:]), "\x00")
		offset, err := strconv.ParseUint(text, 10, 32)
		if err != nil || offset < 4 || offset >= uint64(len(table)) {
			return "", errors.New("invalid COFF section-name offset")
		}
		end := int(offset)
		for end < len(table) && table[end] != 0 {
			end++
		}
		if end == len(table) {
			return "", errors.New("unterminated COFF section name")
		}
		return string(table[offset:uint64(end)]), nil
	}
	return strings.TrimRight(string(raw), "\x00"), nil
}

func Parse(src []byte) (Compatibility, error) {
	var c Compatibility
	if len(src) < 20 || len(src) > MaxObjectSize {
		return c, errors.New("invalid or oversized COFF object")
	}
	machine := binary.LittleEndian.Uint16(src[:2])
	if machine != 0x8664 {
		return c, fmt.Errorf("unsupported COFF architecture 0x%04X (expected AMD64)", machine)
	}
	c.Architecture = "amd64"
	c.Entrypoint = "go"
	sectionCount := int(binary.LittleEndian.Uint16(src[2:4]))
	if sectionCount < 1 || sectionCount > 128 {
		return c, errors.New("invalid COFF section count")
	}
	symbolOffset := binary.LittleEndian.Uint32(src[8:12])
	symbolCount := binary.LittleEndian.Uint32(src[12:16])
	optionSize := binary.LittleEndian.Uint16(src[16:18])
	if optionSize != 0 {
		return c, errors.New("COFF optional header is unsupported for BOFs")
	}
	if symbolCount == 0 || symbolCount > 100000 {
		return c, errors.New("invalid COFF symbol count")
	}
	sectionTable, err := span(src, 20, uint64(sectionCount)*40)
	if err != nil {
		return c, fmt.Errorf("truncated COFF section table: %w", err)
	}
	symbolBytes, err := span(src, uint64(symbolOffset), uint64(symbolCount)*18)
	if err != nil {
		return c, fmt.Errorf("truncated COFF symbol table: %w", err)
	}
	stringOffset := uint64(symbolOffset) + uint64(symbolCount)*18
	lengthBytes, err := span(src, stringOffset, 4)
	if err != nil {
		return c, fmt.Errorf("malformed COFF string table: %w", err)
	}
	stringLength := binary.LittleEndian.Uint32(lengthBytes)
	if stringLength < 4 {
		return c, errors.New("malformed COFF string table length")
	}
	stringsTable, err := span(src, stringOffset, uint64(stringLength))
	if err != nil {
		return c, fmt.Errorf("malformed COFF string table: %w", err)
	}
	c.Sections = make([]Section, sectionCount)
	var relocationTotal int
	for i := 0; i < sectionCount; i++ {
		h := sectionTable[i*40 : (i+1)*40]
		name, err := nameFrom(h[:8], stringsTable)
		if err != nil {
			return c, fmt.Errorf("section %d: %w", i+1, err)
		}
		logical := binary.LittleEndian.Uint32(h[8:12])
		rawSize := binary.LittleEndian.Uint32(h[16:20])
		rawOffset := binary.LittleEndian.Uint32(h[20:24])
		relocOffset := binary.LittleEndian.Uint32(h[24:28])
		relocCount := int(binary.LittleEndian.Uint16(h[32:34]))
		if binary.LittleEndian.Uint16(h[34:36]) != 0 {
			return c, fmt.Errorf("section %s has unsupported line records", name)
		}
		characteristics := binary.LittleEndian.Uint32(h[36:40])
		if characteristics&0x01000000 != 0 {
			return c, fmt.Errorf("section %s uses unsupported relocation overflow", name)
		}
		if logical < rawSize {
			logical = rawSize
		}
		if logical > MaxObjectSize {
			return c, fmt.Errorf("section %s is too large", name)
		}
		var data []byte
		if rawSize > 0 {
			data, err = span(src, uint64(rawOffset), uint64(rawSize))
			if err != nil {
				return c, fmt.Errorf("section %s invalid data offset: %w", name, err)
			}
		}
		if relocCount > 0 {
			if _, err = span(src, uint64(relocOffset), uint64(relocCount)*10); err != nil {
				return c, fmt.Errorf("section %s invalid relocation table: %w", name, err)
			}
		}
		relocationTotal += relocCount
		if relocationTotal > MaxRelocations {
			return c, errors.New("excessive COFF relocation count")
		}
		c.Sections[i] = Section{Name: name, Data: data, Size: logical, Characteristics: characteristics, Relocations: make([]Relocation, relocCount)}
	}
	c.Symbols = make([]Symbol, symbolCount)
	for i := 0; i < int(symbolCount); {
		h := symbolBytes[i*18 : (i+1)*18]
		name, err := nameFrom(h[:8], stringsTable)
		if err != nil {
			return c, fmt.Errorf("symbol %d: %w", i, err)
		}
		section := int16(binary.LittleEndian.Uint16(h[12:14]))
		aux := int(h[17])
		if i+aux >= int(symbolCount) {
			return c, fmt.Errorf("symbol %s has invalid auxiliary count", name)
		}
		if section > int16(sectionCount) || section < -2 {
			return c, fmt.Errorf("symbol %s has invalid section index %d", name, section)
		}
		c.Symbols[i] = Symbol{Name: name, Section: section, Value: binary.LittleEndian.Uint32(h[8:12]), StorageClass: h[16]}
		for j := 1; j <= aux; j++ {
			c.Symbols[i+j].Auxiliary = true
		}
		i += aux + 1
	}
	entry := false
	for _, symbol := range c.Symbols {
		if !symbol.Auxiliary && symbol.Name == "go" && symbol.Section > 0 && symbol.Value < c.Sections[symbol.Section-1].Size {
			entry = true
		}
	}
	if !entry {
		c.Errors = append(c.Errors, "missing go entry point")
	}
	for i := 0; i < sectionCount; i++ {
		h := sectionTable[i*40 : (i+1)*40]
		relocOffset := binary.LittleEndian.Uint32(h[24:28])
		for j := range c.Sections[i].Relocations {
			raw := src[int(relocOffset)+j*10:]
			offset := binary.LittleEndian.Uint32(raw[:4])
			index := binary.LittleEndian.Uint32(raw[4:8])
			kind := binary.LittleEndian.Uint16(raw[8:10])
			width := uint32(4)
			if kind == 1 {
				width = 8
			}
			if kind == 10 {
				width = 2
			}
			if index >= symbolCount || c.Symbols[index].Auxiliary {
				return c, fmt.Errorf("section %s relocation %d has invalid symbol index %d", c.Sections[i].Name, j, index)
			}
			if offset > c.Sections[i].Size || width > c.Sections[i].Size-offset {
				return c, fmt.Errorf("section %s relocation %d exceeds section", c.Sections[i].Name, j)
			}
			reloc := Relocation{Section: i, Offset: offset, SymbolIndex: index, Symbol: c.Symbols[index].Name, Type: kind}
			c.Sections[i].Relocations[j] = reloc
			c.Relocations = append(c.Relocations, reloc)
			if !SupportedRelocation(kind) {
				c.Errors = append(c.Errors, "unsupported AMD64 COFF relocation "+RelocationName(kind))
			}
		}
	}
	seen := make(map[string]bool)
	for _, reloc := range c.Relocations {
		symbol := c.Symbols[reloc.SymbolIndex]
		if symbol.Section != 0 || seen[symbol.Name] {
			continue
		}
		seen[symbol.Name] = true
		imp, kind, err := ClassifyImport(symbol.Name)
		if err != nil {
			c.Errors = append(c.Errors, err.Error())
			c.UnknownImports = append(c.UnknownImports, Import{Name: symbol.Name})
			continue
		}
		switch kind {
		case "windows":
			c.WindowsImports = append(c.WindowsImports, imp)
		case "beacon":
			c.BeaconImports = append(c.BeaconImports, imp)
		}
	}
	c.Supported = len(c.Errors) == 0
	return c, nil
}

func ClassifyImport(name string) (Import, string, error) {
	imp := Import{Name: name}
	bare := name
	if strings.HasPrefix(bare, "__imp_") {
		bare = strings.TrimPrefix(bare, "__imp_")
		imp.Indirect = true
	}
	if strings.HasPrefix(bare, "Beacon") {
		if !SupportedBeacon(bare) {
			return imp, "", fmt.Errorf("unsupported Beacon API import: %s", bare)
		}
		imp.Export = bare
		return imp, "beacon", nil
	}
	library, export, ok := strings.Cut(bare, "$")
	if ok && library != "" && export != "" && !strings.ContainsAny(library, "/\\.:\x00") && !strings.ContainsAny(export, "/\\$\x00") {
		imp.Library = library + ".dll"
		imp.Export = export
		return imp, "windows", nil
	}
	return imp, "", fmt.Errorf("unsupported external symbol: %s", name)
}
