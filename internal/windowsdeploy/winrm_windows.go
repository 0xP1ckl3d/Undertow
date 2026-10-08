//go:build windows

package windowsdeploy

import (
	"encoding/xml"
	"errors"
	"fmt"
	"runtime"
	"strings"
)

const winRMProcessResource = "http://schemas.microsoft.com/wbem/wsman/1/wmi/root/cimv2/Win32_Process"

type winRMProcessResponse struct {
	ProcessID   uint32 `xml:"ProcessId"`
	ReturnValue uint32 `xml:"ReturnValue"`
}

func winRMProcessInput(path string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	command := replacer.Replace(windowsCommandLine(path))
	return `<p:Create_INPUT xmlns:p="` + winRMProcessResource + `"><p:CommandLine>` + command + `</p:CommandLine></p:Create_INPUT>`
}

func parseWinRMProcessResponse(value string) (uint32, error) {
	var response winRMProcessResponse
	if err := xml.Unmarshal([]byte(value), &response); err != nil {
		return 0, fmt.Errorf("parse WSMan process response: %w", err)
	}
	if response.ReturnValue != 0 {
		return 0, fmt.Errorf("Win32_Process.Create returned %d", response.ReturnValue)
	}
	if response.ProcessID == 0 {
		return 0, errors.New("Win32_Process.Create returned no process ID")
	}
	return response.ProcessID, nil
}

func runWinRMProcess(target, path string) (uint32, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procCoInitializeEx.Call(0, 0)
	initialized := !failedHRESULT(hr)
	if failedHRESULT(hr) && uint32(hr) != rpcEChangedMode {
		return 0, hresultError("initialize COM", hr)
	}
	if initialized {
		defer procCoUninitialize.Call()
	}

	wsman, err := createDispatch("WSMan.Automation")
	if err != nil {
		return 0, fmt.Errorf("create WSMan automation object: %w", err)
	}
	defer wsman.release()
	session, err := objectMethod(wsman, "CreateSession", target)
	if err != nil {
		return 0, fmt.Errorf("create remote WSMan session: %w", err)
	}
	defer session.release()
	result, err := session.invoke("Invoke", dispatchMethod, "Create", winRMProcessResource, winRMProcessInput(path), 0)
	if err != nil {
		return 0, fmt.Errorf("invoke Win32_Process.Create over WSMan: %w", err)
	}
	text, err := variantString(&result)
	if err != nil {
		return 0, err
	}
	return parseWinRMProcessResponse(text)
}
