//go:build windows

package authcontext

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type WindowsBackend struct{}
type WindowsToken struct {
	handle      windows.Token
	source      string
	launchReady bool
}

func (t *WindowsToken) Native() windows.Token { return t.handle }
func (t *WindowsToken) CanLaunch() bool       { return t.launchReady }
func (t *WindowsToken) Close() error          { return t.handle.Close() }

const processLaunchAccess = windows.TOKEN_QUERY | windows.TOKEN_DUPLICATE | windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_IMPERSONATE | windows.TOKEN_ADJUST_DEFAULT
const threadTokenAccess = windows.TOKEN_QUERY | windows.TOKEN_DUPLICATE | windows.TOKEN_IMPERSONATE

func (t *WindowsToken) Duplicate() (Token, error) {
	var h windows.Token
	ready := t.launchReady
	var err error
	if ready {
		err = windows.DuplicateTokenEx(t.handle, windows.TOKEN_ALL_ACCESS, nil, windows.SecurityImpersonation, windows.TokenPrimary, &h)
		if err != nil {
			err = windows.DuplicateTokenEx(t.handle, processLaunchAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &h)
		}
	} else {
		err = windows.DuplicateTokenEx(t.handle, threadTokenAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &h)
	}
	if err != nil {
		return nil, fmt.Errorf("duplicate authentication token: %w", err)
	}
	return &WindowsToken{handle: h, source: t.source, launchReady: ready}, nil
}

func duplicateCandidate(h windows.Token, source string) (*WindowsToken, error) {
	original := &WindowsToken{handle: h, source: source, launchReady: true}
	if copied, err := original.Duplicate(); err == nil {
		return copied.(*WindowsToken), nil
	}
	original.launchReady = false
	copied, err := original.Duplicate()
	if err != nil {
		return nil, err
	}
	return copied.(*WindowsToken), nil
}
func tokenUint(t windows.Token, class uint32) (uint32, error) {
	var v, n uint32
	err := windows.GetTokenInformation(t, class, (*byte)(unsafe.Pointer(&v)), 4, &n)
	return v, err
}
func (t *WindowsToken) Metadata() (Metadata, error) {
	u, err := t.handle.GetTokenUser()
	if err != nil {
		return Metadata{}, err
	}
	user, domain, _, err := u.User.Sid.LookupAccount("")
	if err != nil {
		user = u.User.Sid.String()
		domain = ""
	}
	typ, err := tokenUint(t.handle, windows.TokenType)
	if err != nil {
		return Metadata{}, err
	}
	session, err := tokenUint(t.handle, windows.TokenSessionId)
	if err != nil {
		return Metadata{}, err
	}
	elevated, err := tokenUint(t.handle, windows.TokenElevation)
	if err != nil {
		return Metadata{}, err
	}
	elevation, err := tokenUint(t.handle, windows.TokenElevationType)
	if err != nil {
		return Metadata{}, err
	}
	var needed uint32
	_ = windows.GetTokenInformation(t.handle, windows.TokenIntegrityLevel, nil, 0, &needed)
	if needed == 0 {
		return Metadata{}, errors.New("token integrity metadata unavailable")
	}
	buffer := make([]byte, needed)
	if err := windows.GetTokenInformation(t.handle, windows.TokenIntegrityLevel, &buffer[0], needed, &needed); err != nil {
		return Metadata{}, err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0]))
	sid := label.Label.Sid
	rid := sid.SubAuthority(uint32(sid.SubAuthorityCount() - 1))
	integrity := "untrusted"
	switch {
	case rid >= 0x5000:
		integrity = "protected"
	case rid >= 0x4000:
		integrity = "system"
	case rid >= 0x3000:
		integrity = "high"
	case rid >= 0x2100:
		integrity = "medium-plus"
	case rid >= 0x2000:
		integrity = "medium"
	case rid >= 0x1000:
		integrity = "low"
	}
	tokenType, level := "primary", "not-applicable"
	if typ == windows.TokenImpersonation {
		tokenType = "impersonation"
		v, err := tokenUint(t.handle, windows.TokenImpersonationLevel)
		if err != nil {
			return Metadata{}, err
		}
		levels := []string{"anonymous", "identification", "impersonation", "delegation"}
		if v >= uint32(len(levels)) {
			return Metadata{}, errors.New("invalid impersonation level")
		}
		level = levels[v]
	}
	elevations := map[uint32]string{1: "default", 2: "full", 3: "limited"}
	identity := user
	if domain != "" {
		identity = domain + "\\" + user
	}
	return Metadata{Identity: identity, Domain: domain, User: user, TokenType: tokenType, ProcessLaunchReady: t.launchReady, ImpersonationLevel: level, IntegrityLevel: integrity, SessionID: session, Elevated: elevated != 0, ElevationType: elevations[elevation], Source: t.source}, nil
}

// Discovery uses ordinary process-token access. It does not enable privileges,
// query linked elevated tokens, or bypass process/token access checks.
func (WindowsBackend) Discover(ctx context.Context) ([]Token, error) {
	// Always include the original agent identity before the bounded process
	// snapshot, even on hosts with hundreds of accessible process tokens.
	var own windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.MAXIMUM_ALLOWED, &own)
	if err != nil {
		err = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &own)
	}
	if err != nil {
		return nil, fmt.Errorf("open agent process token: %w", err)
	}
	duplicate, err := duplicateCandidate(own, "agent process")
	_ = own.Close()
	if err != nil {
		return nil, err
	}
	result := []Token{duplicate}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return result, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return result, err
	}
	for {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if entry.ProcessID != uint32(os.Getpid()) {
			process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
			if err == nil {
				var h windows.Token
				openErr := windows.OpenProcessToken(process, windows.MAXIMUM_ALLOWED, &h)
				if openErr != nil {
					openErr = windows.OpenProcessToken(process, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &h)
				}
				if openErr == nil {
					source := fmt.Sprintf("process:%d (%s)", entry.ProcessID, windows.UTF16ToString(entry.ExeFile[:]))
					dup, err := duplicateCandidate(h, source)
					_ = h.Close()
					if err == nil {
						result = append(result, dup)
					}
				}
				_ = windows.CloseHandle(process)
			}
		}
		if len(result) >= Limit {
			break
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return result, err
			}
			break
		}
	}
	return result, nil
}

var logonUser = windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW")

func (WindowsBackend) Logon(ctx context.Context, r LogonRequest) (Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.Reference != "" {
		return nil, errors.New("agent credential provider is not configured")
	}
	if r.User == "" || len(r.User) > 256 || len(r.Domain) > 256 || len(r.Password) > 512 || strings.ContainsRune(r.User+r.Domain+r.Password, 0) {
		return nil, errors.New("invalid logon request")
	}
	types := map[string]uint32{"interactive": 2, "network": 3, "batch": 4, "new_credentials": 9}
	logonType, ok := types[r.LogonType]
	if !ok {
		return nil, errors.New("supported logon types: interactive, network, batch, new_credentials")
	}
	user, _ := windows.UTF16FromString(r.User)
	password, _ := windows.UTF16FromString(r.Password)
	defer func() { clear(password); runtime.KeepAlive(password) }()
	var domain *uint16
	var domainBuffer []uint16
	if r.Domain != "" {
		domainBuffer, _ = windows.UTF16FromString(r.Domain)
		domain = &domainBuffer[0]
	}
	provider := uintptr(0)
	if logonType == 9 {
		provider = 3
	}
	var h windows.Token
	result, _, err := logonUser.Call(uintptr(unsafe.Pointer(&user[0])), uintptr(unsafe.Pointer(domain)), uintptr(unsafe.Pointer(&password[0])), uintptr(logonType), provider, uintptr(unsafe.Pointer(&h)))
	runtime.KeepAlive(domainBuffer)
	runtime.KeepAlive(user)
	runtime.KeepAlive(password)
	if result == 0 {
		return nil, fmt.Errorf("Windows logon failed: %w", err)
	}
	defer h.Close()
	return duplicateCandidate(h, "logon:"+r.LogonType)
}
