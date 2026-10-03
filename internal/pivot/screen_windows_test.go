//go:build windows

package pivot

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestEncodeScreenPNG(t *testing.T) {
	encoded, err := encodeScreenPNG([]byte{255, 0, 0, 255, 0, 255, 0, 255}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if image.Bounds().Dx() != 2 || image.Bounds().Dy() != 1 {
		t.Fatalf("dimensions: %v", image.Bounds())
	}
	r, g, b, _ := image.At(0, 0).RGBA()
	if r != 65535 || g != 0 || b != 0 {
		t.Fatalf("first pixel: %d %d %d", r, g, b)
	}
}

func TestLiveWindowsScreenCapture(t *testing.T) {
	if os.Getenv("UNDERTOW_TEST_LIVE_SCREEN") != "1" {
		t.Skip("set UNDERTOW_TEST_LIVE_SCREEN=1 to capture this desktop")
	}
	screens, err := listScreens()
	if err != nil {
		t.Fatal(err)
	}
	for _, screen := range screens {
		data, err := captureScreen(screen.Number)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Bounds().Dx() != screen.Width || decoded.Bounds().Dy() != screen.Height {
			t.Fatalf("screen %d size mismatch", screen.Number)
		}
		t.Logf("screen %d %s %dx%d PNG %d bytes", screen.Number, screen.Name, screen.Width, screen.Height, len(data))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	operator, agent := execTestMuxPair(ctx)
	defer operator.Close()
	defer agent.Close()
	go ServeAgentWithCapabilities(ctx, agent, DefaultCapabilities())
	result, err := ExecuteRequest(ctx, operator, ExecRequest{Builtin: "screens"})
	if err != nil || result.Error != "" {
		t.Fatalf("screen listing over agent stream: %+v %v", result, err)
	}
	path := filepath.Join(t.TempDir(), "screen.png")
	transfer, err := TransferFile(ctx, operator, "test-agent", "screenshot", path, strconv.Itoa(screens[0].Number))
	if err != nil || !transfer.OK {
		t.Fatalf("screen transfer: %+v %v", transfer, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("transferred PNG: %v", err)
	}
}
