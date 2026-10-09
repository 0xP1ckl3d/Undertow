package pivot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// WASMHostNamespace is the public import name. Its ABI is frozen for version 1.
const WASMHostNamespace = "undertow_host_v1"

const wasmHostValueLimit = 64 << 10

type wasmHostRequest struct {
	Path      string `json:"path"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Address   string `json:"address"`
	Network   string `json:"network"`
	Handle    uint32 `json:"handle"`
	Data      string `json:"data"`
	Offset    int64  `json:"offset"`
	Limit     int    `json:"limit"`
	Depth     int    `json:"depth"`
	TimeoutMS int    `json:"timeout_ms"`
}

func instantiateWASMHost(ctx context.Context, r wazero.Runtime) (func(), error) {
	lastError := ""
	state := &wasmSocketState{connections: make(map[uint32]net.Conn)}
	b := r.NewHostModuleBuilder(WASMHostNamespace)
	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, opPtr, opLen, inputPtr, inputLen, outPtr, outCap uint32) int32 {
		lastError = ""
		memory := mod.Memory()
		if opLen == 0 || opLen > 64 || inputLen > wasmHostValueLimit || outCap > wasmHostValueLimit {
			lastError = "invalid call length"
			return -1
		}
		opBytes, ok := memory.Read(opPtr, opLen)
		if !ok {
			lastError = "invalid operation pointer"
			return -1
		}
		inputBytes, ok := memory.Read(inputPtr, inputLen)
		if !ok {
			lastError = "invalid input pointer"
			return -1
		}
		// Copy inputs before writing guest memory: source and destination may overlap.
		op, input := string(opBytes), append([]byte(nil), inputBytes...)
		if uint64(outPtr)+uint64(outCap) > uint64(memory.Size()) {
			lastError = "invalid output pointer"
			return -1
		}
		var req wasmHostRequest
		if len(input) != 0 {
			if err := json.Unmarshal(input, &req); err != nil {
				lastError = "invalid JSON input: " + err.Error()
				return -1
			}
		}
		opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		value, err := state.operation(opCtx, op, req)
		if err != nil {
			lastError = err.Error()
			if errors.Is(err, errWASMUnsupported) {
				return -2
			}
			return -3
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			lastError = err.Error()
			return -3
		}
		if len(encoded) > wasmHostValueLimit || len(encoded) > int(outCap) {
			lastError = fmt.Sprintf("result needs %d bytes (maximum %d)", len(encoded), wasmHostValueLimit)
			return -4
		}
		if !memory.Write(outPtr, encoded) {
			lastError = "invalid output pointer"
			return -1
		}
		return int32(len(encoded))
	}).Export("call")
	b.NewFunctionBuilder().WithFunc(func(_ context.Context, mod api.Module, outPtr, outCap uint32) int32 {
		if outCap > 4096 || uint64(outPtr)+uint64(outCap) > uint64(mod.Memory().Size()) {
			return -1
		}
		if len(lastError) > int(outCap) {
			return -4
		}
		if !mod.Memory().Write(outPtr, []byte(lastError)) {
			return -1
		}
		return int32(len(lastError))
	}).Export("last_error")
	_, err := b.Instantiate(ctx)
	if err != nil {
		state.closeAll()
		return nil, err
	}
	return state.closeAll, nil
}

var errWASMUnsupported = errors.New("operation unsupported on this platform")

func wasmHostOperation(ctx context.Context, op string, req wasmHostRequest) (any, error) {
	switch op {
	case "version":
		return map[string]any{"namespace": WASMHostNamespace, "major": 1}, nil
	case "system":
		host, _ := os.Hostname()
		cwd, _ := os.Getwd()
		identity := map[string]any{"user": os.Getenv("USER"), "uid": fmt.Sprint(os.Getuid()), "gid": fmt.Sprint(os.Getgid()), "home": os.Getenv("HOME")}
		if current, err := user.Current(); err == nil {
			identity["user"], identity["uid"], identity["gid"], identity["home"] = current.Username, current.Uid, current.Gid, current.HomeDir
		}
		if metadata, selected, err := operationTokenMetadata(ctx); selected {
			if err != nil {
				return nil, err
			}
			identity["user"], identity["uid"], identity["gid"], identity["home"] = metadata.Identity, "", "", ""
			if current, err := user.Lookup(metadata.Identity); err == nil {
				identity["uid"], identity["gid"], identity["home"] = current.Uid, current.Gid, current.HomeDir
			}
		}
		identity["os"], identity["arch"], identity["hostname"], identity["cwd"], identity["pid"] = runtime.GOOS, runtime.GOARCH, host, cwd, os.Getpid()
		return identity, nil
	case "environment":
		if req.Name != "" {
			value, ok := os.LookupEnv(req.Name)
			return map[string]any{"name": req.Name, "value": value, "exists": ok}, nil
		}
		values := os.Environ()
		sort.Strings(values)
		return values, nil
	case "interfaces":
		ifaces, err := net.Interfaces()
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(ifaces))
		for _, iface := range ifaces {
			addresses, _ := iface.Addrs()
			list := make([]string, 0, len(addresses))
			for _, addr := range addresses {
				list = append(list, addr.String())
			}
			out = append(out, map[string]any{"name": iface.Name, "index": iface.Index, "mtu": iface.MTU, "flags": iface.Flags.String(), "mac": iface.HardwareAddr.String(), "addresses": list})
		}
		return out, nil
	case "processes", "privileges", "routes", "dns_config", "users", "groups", "neighbours", "connections", "services", "startup":
		return wasmPlatformInventory(ctx, op)
	case "fs.list":
		path, err := wasmPath(req.Path)
		if err != nil {
			return nil, err
		}
		entries, err := wasmReadDir(path, 512)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			out = append(out, map[string]any{"name": e.Name(), "directory": e.IsDir(), "type": e.Type().String()})
		}
		return out, nil
	case "fs.stat":
		path, err := wasmPath(req.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		return wasmFileInfo(path, info), nil
	case "fs.read":
		path, err := wasmPath(req.Path)
		if err != nil {
			return nil, err
		}
		if req.Offset < 0 {
			return nil, errors.New("negative offset")
		}
		limit := req.Limit
		if limit <= 0 || limit > 32<<10 {
			limit = 32 << 10
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if _, err = f.Seek(req.Offset, io.SeekStart); err != nil {
			return nil, err
		}
		buf := make([]byte, limit)
		n, err := f.Read(buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return map[string]any{"data_base64": base64.StdEncoding.EncodeToString(buf[:n]), "bytes": n, "offset": req.Offset}, nil
	case "fs.walk":
		path, err := wasmPath(req.Path)
		if err != nil {
			return nil, err
		}
		return wasmWalk(ctx, path, req.Depth)
	case "dns.resolve":
		if req.Host == "" || len(req.Host) > 253 {
			return nil, errors.New("invalid host")
		}
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupIPAddr(queryCtx, req.Host)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(ips))
		for _, ip := range ips {
			out = append(out, ip.IP.String())
		}
		return out, nil
	case "net.tcp_exchange", "net.udp_exchange":
		return wasmExchange(ctx, op, req)
	case "registry.read":
		return wasmRegistryRead(ctx, req)
	case "windows.service_names", "windows.service_config", "windows.file_acl":
		return wasmWindowsAudit(ctx, op, req)
	default:
		return nil, errWASMUnsupported
	}
}

func wasmPath(path string) (string, error) {
	if path == "" || len(path) > 4096 || strings.IndexByte(path, 0) >= 0 {
		return "", errors.New("invalid path")
	}
	return filepath.Clean(path), nil
}

func wasmFileInfo(path string, info os.FileInfo) map[string]any {
	return map[string]any{"path": path, "size": info.Size(), "mode": info.Mode().String(), "permissions": uint32(info.Mode().Perm()), "directory": info.IsDir(), "modified": info.ModTime().UTC().Format(time.RFC3339), "symlink": info.Mode()&os.ModeSymlink != 0}
}

func wasmReadDir(path string, limit int) ([]os.DirEntry, error) {
	dir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(limit)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return entries, err
}

func wasmWalk(ctx context.Context, root string, depth int) (any, error) {
	if depth <= 0 {
		depth = 2
	}
	if depth > 5 {
		depth = 5
	}
	root = filepath.Clean(root)
	if _, err := os.Lstat(root); err != nil {
		return nil, err
	}
	type item struct {
		path  string
		depth int
	}
	queue := []item{{root, 0}}
	out := make([]map[string]any, 0)
	for len(queue) > 0 && len(out) < 256 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := queue[0]
		queue = queue[1:]
		info, err := os.Lstat(current.path)
		if err != nil {
			continue
		}
		out = append(out, wasmFileInfo(current.path, info))
		if !info.IsDir() || current.depth >= depth {
			continue
		}
		remaining := 256 - len(out) - len(queue)
		if remaining <= 0 {
			continue
		}
		entries, err := wasmReadDir(current.path, remaining)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if len(queue)+len(out) >= 256 {
				break
			}
			queue = append(queue, item{filepath.Join(current.path, e.Name()), current.depth + 1})
		}
	}
	return out, nil
}

func wasmExchange(ctx context.Context, op string, req wasmHostRequest) (any, error) {
	if req.Address == "" || len(req.Address) > 512 {
		return nil, errors.New("invalid address")
	}
	if _, _, err := net.SplitHostPort(req.Address); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil || len(data) > 8192 {
		return nil, errors.New("data must be base64 and at most 8192 bytes")
	}
	limit := req.Limit
	if limit <= 0 || limit > 8192 {
		limit = 8192
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	network := "tcp"
	if op == "net.udp_exchange" {
		network = "udp"
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, network, req.Address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if len(data) > 0 || op == "net.udp_exchange" {
		if _, err := conn.Write(data); err != nil {
			return nil, err
		}
	}
	if op == "net.tcp_exchange" && len(data) == 0 {
		return map[string]any{"connected": true, "data_base64": "", "bytes": 0}, nil
	}
	buf := make([]byte, limit)
	n, err := conn.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return map[string]any{"connected": true, "data_base64": base64.StdEncoding.EncodeToString(buf[:n]), "bytes": n}, nil
}
