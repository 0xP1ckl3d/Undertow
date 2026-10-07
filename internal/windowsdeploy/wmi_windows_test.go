//go:build windows

package windowsdeploy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"
)

func TestVariantMatchesWindowsAMD64ABI(t *testing.T) {
	if runtime.GOARCH == "amd64" && unsafe.Sizeof(variant{}) != 24 {
		t.Fatalf("VARIANT size = %d, want 24", unsafe.Sizeof(variant{}))
	}
}

func TestWMIComCreateLocal(t *testing.T) {
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "whoami.exe")
	if _, err := runWMIProcess(".", quoteWindowsArgument(path)); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsCommandLineQuotesInstallPath(t *testing.T) {
	command := windowsCommandLine(`C:\Windows\System32\schtasks.exe`, "/TR", `C:\Program Data\Undertow\agent.exe`)
	want := `C:\Windows\System32\schtasks.exe /TR "C:\Program Data\Undertow\agent.exe"`
	if command != want {
		t.Fatalf("command line = %q, want %q", command, want)
	}
}

func TestWMIComConnectLocal(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, 0)
	initialized := !failedHRESULT(hr)
	if failedHRESULT(hr) && uint32(hr) != rpcEChangedMode {
		t.Fatal(hresultError("initialize COM", hr))
	}
	if initialized {
		defer procCoUninitialize.Call()
	}
	locator, err := createDispatch("WbemScripting.SWbemLocator")
	if err != nil {
		t.Fatal(err)
	}
	defer locator.release()
	service, err := objectMethod(locator, "ConnectServer", ".", `root\cimv2`)
	if err != nil {
		t.Fatal(err)
	}
	service.release()
}
