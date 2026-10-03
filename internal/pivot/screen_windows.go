//go:build windows

package pivot

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"path/filepath"
	"sort"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var screenUser32 = windows.NewLazySystemDLL("user32.dll")
var screenGDI32 = windows.NewLazySystemDLL("gdi32.dll")
var enumDisplayMonitors = screenUser32.NewProc("EnumDisplayMonitors")
var enumWindows = screenUser32.NewProc("EnumWindows")
var getMonitorInfo = screenUser32.NewProc("GetMonitorInfoW")
var isWindowVisible = screenUser32.NewProc("IsWindowVisible")
var isIconic = screenUser32.NewProc("IsIconic")
var getWindowRect = screenUser32.NewProc("GetWindowRect")
var getWindowText = screenUser32.NewProc("GetWindowTextW")
var getWindowThreadProcessID = screenUser32.NewProc("GetWindowThreadProcessId")
var getDC = screenUser32.NewProc("GetDC")
var releaseDC = screenUser32.NewProc("ReleaseDC")
var createCompatibleDC = screenGDI32.NewProc("CreateCompatibleDC")
var deleteDC = screenGDI32.NewProc("DeleteDC")
var createCompatibleBitmap = screenGDI32.NewProc("CreateCompatibleBitmap")
var selectObject = screenGDI32.NewProc("SelectObject")
var deleteObject = screenGDI32.NewProc("DeleteObject")
var bitBlt = screenGDI32.NewProc("BitBlt")
var getDIBits = screenGDI32.NewProc("GetDIBits")

type screenRect struct{ Left, Top, Right, Bottom int32 }
type monitorInfo struct {
	Size    uint32
	Monitor screenRect
	Work    screenRect
	Flags   uint32
	Device  [32]uint16
}
type monitorSnapshot struct {
	handle uintptr
	info   monitorInfo
}
type bitmapHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func displayMonitors() ([]monitorSnapshot, error) {
	var monitors []monitorSnapshot
	callback := syscall.NewCallback(func(handle, _hdc, _rect, _data uintptr) uintptr {
		info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
		if ok, _, _ := getMonitorInfo.Call(handle, uintptr(unsafe.Pointer(&info))); ok != 0 {
			monitors = append(monitors, monitorSnapshot{handle: handle, info: info})
		}
		return 1
	})
	if ok, _, err := enumDisplayMonitors.Call(0, 0, callback, 0); ok == 0 {
		return nil, fmt.Errorf("enumerate displays: %w", err)
	}
	sort.Slice(monitors, func(i, j int) bool {
		a, b := monitors[i].info.Monitor, monitors[j].info.Monitor
		if a.Left != b.Left {
			return a.Left < b.Left
		}
		return a.Top < b.Top
	})
	if len(monitors) == 0 {
		return nil, errors.New("no displays are available in the agent desktop session")
	}
	return monitors, nil
}

func listScreens() ([]ScreenInfo, error) {
	monitors, err := displayMonitors()
	if err != nil {
		return nil, err
	}
	visible := visibleDesktopWindows()
	screens := make([]ScreenInfo, 0, len(monitors))
	for i, monitor := range monitors {
		r := monitor.info.Monitor
		screen := ScreenInfo{Number: i + 1, Name: windows.UTF16ToString(monitor.info.Device[:]), Width: int(r.Right - r.Left), Height: int(r.Bottom - r.Top)}
		for _, window := range visible {
			if intersects(r, window.rect) {
				screen.Foreground = window.title
				break
			}
		}
		screens = append(screens, screen)
	}
	return screens, nil
}

type foregroundInfo struct {
	rect  screenRect
	title string
}

func visibleDesktopWindows() []foregroundInfo {
	var windows []foregroundInfo
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		visible, _, _ := isWindowVisible.Call(hwnd)
		minimized, _, _ := isIconic.Call(hwnd)
		if visible != 0 && minimized == 0 {
			if window := desktopWindowInfo(hwnd); window.title != "" {
				windows = append(windows, window)
			}
		}
		return 1
	})
	enumWindows.Call(callback, 0)
	return windows
}

func desktopWindowInfo(hwnd uintptr) foregroundInfo {
	var rect screenRect
	if ok, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return foregroundInfo{}
	}
	var title [512]uint16
	n, _, _ := getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	label := windows.UTF16ToString(title[:n])
	if label == "" {
		return foregroundInfo{}
	}
	var pid uint32
	getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		var name [1024]uint16
		length := uint32(len(name))
		if windows.QueryFullProcessImageName(process, 0, &name[0], &length) == nil {
			processName := filepath.Base(windows.UTF16ToString(name[:length]))
			if label == "" {
				label = processName
			} else {
				label = processName + " — " + label
			}
		}
		windows.CloseHandle(process)
	}
	return foregroundInfo{rect: rect, title: label}
}

func intersects(a, b screenRect) bool {
	return a.Left < b.Right && b.Left < a.Right && a.Top < b.Bottom && b.Top < a.Bottom
}

func captureScreen(number int) ([]byte, error) {
	monitors, err := displayMonitors()
	if err != nil {
		return nil, err
	}
	if number < 1 || number > len(monitors) {
		return nil, fmt.Errorf("screen %d is unavailable; use screens to list displays", number)
	}
	r := monitors[number-1].info.Monitor
	width, height := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if width <= 0 || height <= 0 || int64(width)*int64(height) > 32<<20 {
		return nil, errors.New("invalid or oversized display")
	}
	dc, _, _ := getDC.Call(0)
	if dc == 0 {
		return nil, errors.New("desktop capture is unavailable in this agent session")
	}
	defer releaseDC.Call(0, dc)
	memoryDC, _, _ := createCompatibleDC.Call(dc)
	if memoryDC == 0 {
		return nil, errors.New("cannot create capture context")
	}
	defer deleteDC.Call(memoryDC)
	bitmap, _, _ := createCompatibleBitmap.Call(dc, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, errors.New("cannot create capture bitmap")
	}
	defer deleteObject.Call(bitmap)
	previous, _, _ := selectObject.Call(memoryDC, bitmap)
	if previous == 0 {
		return nil, errors.New("cannot select capture bitmap")
	}
	selected := true
	defer func() {
		if selected {
			selectObject.Call(memoryDC, previous)
		}
	}()
	const sourceCopy = 0x00CC0020
	const captureLayered = 0x40000000
	if ok, _, _ := bitBlt.Call(memoryDC, 0, 0, uintptr(width), uintptr(height), dc, uintptr(r.Left), uintptr(r.Top), sourceCopy|captureLayered); ok == 0 {
		return nil, errors.New("desktop capture failed; an interactive desktop may be required")
	}
	selectObject.Call(memoryDC, previous)
	selected = false
	header := bitmapHeader{Size: uint32(unsafe.Sizeof(bitmapHeader{})), Width: int32(width), Height: -int32(height), Planes: 1, BitCount: 32}
	bgra := make([]byte, width*height*4)
	if rows, _, _ := getDIBits.Call(dc, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&bgra[0])), uintptr(unsafe.Pointer(&header)), 0); rows != uintptr(height) {
		return nil, errors.New("cannot read captured pixels")
	}
	for i := 0; i < len(bgra); i += 4 {
		bgra[i], bgra[i+2], bgra[i+3] = bgra[i+2], bgra[i], 255
	}
	return encodeScreenPNG(bgra, width, height)
}

func encodeScreenPNG(pixels []byte, width, height int) ([]byte, error) {
	var output bytes.Buffer
	output.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})
	var header [13]byte
	binary.BigEndian.PutUint32(header[0:4], uint32(width))
	binary.BigEndian.PutUint32(header[4:8], uint32(height))
	header[8], header[9] = 8, 6 // 8-bit RGBA
	writePNGChunk(&output, "IHDR", header[:])
	var compressed bytes.Buffer
	encoder := zlib.NewWriter(&compressed)
	for row := 0; row < height; row++ {
		if _, err := encoder.Write([]byte{0}); err != nil {
			return nil, err
		}
		if _, err := encoder.Write(pixels[row*width*4 : (row+1)*width*4]); err != nil {
			return nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	writePNGChunk(&output, "IDAT", compressed.Bytes())
	writePNGChunk(&output, "IEND", nil)
	return output.Bytes(), nil
}

func writePNGChunk(output *bytes.Buffer, kind string, data []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	output.Write(length[:])
	output.WriteString(kind)
	output.Write(data)
	checksum := crc32.ChecksumIEEE(append([]byte(kind), data...))
	binary.BigEndian.PutUint32(length[:], checksum)
	output.Write(length[:])
}
