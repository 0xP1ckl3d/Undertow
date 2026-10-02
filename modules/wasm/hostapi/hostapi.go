//go:build wasip1

package hostapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

//go:wasmimport undertow_host_v1 call
func hostCall(opPtr, opLen, inputPtr, inputLen, outPtr, outCap uint32) int32

//go:wasmimport undertow_host_v1 last_error
func hostLastError(outPtr, outCap uint32) int32

func ptr(bytes []byte) uint32 {
	if len(bytes) == 0 {
		return 0
	}
	return uint32(uintptr(unsafe.Pointer(&bytes[0])))
}

// Call invokes one versioned host primitive and decodes its JSON result.
func Call(operation string, request any, result any) error {
	var input []byte
	if request != nil {
		var err error
		input, err = json.Marshal(request)
		if err != nil {
			return err
		}
	}
	op := []byte(operation)
	out := make([]byte, 64<<10)
	n := hostCall(ptr(op), uint32(len(op)), ptr(input), uint32(len(input)), ptr(out), uint32(len(out)))
	runtime.KeepAlive(op)
	runtime.KeepAlive(input)
	if n < 0 {
		message := make([]byte, 4096)
		length := hostLastError(ptr(message), uint32(len(message)))
		runtime.KeepAlive(message)
		if length > 0 {
			return fmt.Errorf("%s: %s (code %d)", operation, message[:length], n)
		}
		return fmt.Errorf("%s: host error %d", operation, n)
	}
	if result == nil {
		return nil
	}
	if int(n) > len(out) {
		return errors.New("invalid host result length")
	}
	return json.Unmarshal(out[:n], result)
}

func Text(operation string, request any) (string, error) {
	var out string
	err := Call(operation, request, &out)
	return out, err
}
