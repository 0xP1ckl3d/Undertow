package assembly

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
)

const MaxSize = 8 << 20

type Metadata struct {
	Architecture string
	Executable   bool
}

// Inspect accepts pure-IL PE assemblies that the 64-bit .NET Framework CLR can load.
// Execution still performs CLR metadata and dependency resolution in the worker.
func Inspect(data []byte) (Metadata, error) {
	if len(data) < 256 || len(data) > MaxSize || !bytes.HasPrefix(data, []byte("MZ")) {
		return Metadata{}, errors.New("assembly must be a managed PE file of at most 8 MiB")
	}
	file, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return Metadata{}, fmt.Errorf("invalid assembly PE: %w", err)
	}
	defer file.Close()
	var directory pe.DataDirectory
	switch header := file.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		if file.Machine != pe.IMAGE_FILE_MACHINE_I386 {
			return Metadata{}, fmt.Errorf("unsupported assembly machine %#x", file.Machine)
		}
		directory = header.DataDirectory[14]
	case *pe.OptionalHeader64:
		if file.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			return Metadata{}, fmt.Errorf("unsupported assembly machine %#x", file.Machine)
		}
		directory = header.DataDirectory[14]
	default:
		return Metadata{}, errors.New("unsupported assembly PE optional header")
	}
	if directory.VirtualAddress == 0 || directory.Size < 20 {
		return Metadata{}, errors.New("PE has no .NET CLR header")
	}
	for _, section := range file.Sections {
		start, end := uint64(section.VirtualAddress), uint64(section.VirtualAddress)+uint64(section.Size)
		rva := uint64(directory.VirtualAddress)
		if rva < start || rva+20 > end {
			continue
		}
		offset := uint64(section.Offset) + rva - start
		if offset+20 > uint64(len(data)) {
			return Metadata{}, errors.New("truncated .NET CLR header")
		}
		flags := binary.LittleEndian.Uint32(data[offset+16:])
		if flags&1 == 0 || flags&2 != 0 {
			return Metadata{}, errors.New("assembly must be pure IL and not require a 32-bit process")
		}
		return Metadata{Architecture: "amd64", Executable: file.Characteristics&pe.IMAGE_FILE_DLL == 0}, nil
	}
	return Metadata{}, errors.New(".NET CLR header is outside PE sections")
}
