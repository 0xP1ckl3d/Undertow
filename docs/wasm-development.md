# Developing Undertow WASM modules

Undertow runs a `wasip1/wasm` module in memory on the selected agent. The agent receives the compiled bytes through the Undertow session, instantiates them with wazero, and discards the instance when it exits. The target needs no Go toolchain, interpreter, module file, or native executable loader. Every module, including the [packaged examples](../modules/wasm/README.md), gets the same host imports. Host operations run with the operating system privileges of the agent process.

For repeated use, place a `.wasm` file and optional help sidecar in the local [module bank](module-bank.md). The console preloads packaged WASM modules as commands such as `wasm-triage`; `load wasm FILE [NAME]` registers one during a session.

For repeated use, place a `.wasm` file and optional help sidecar in the local [module bank](module-bank.md). The console preloads packaged WASM modules as commands such as `wasm-triage`; `load wasm FILE [NAME]` registers one during a session.

## Quick start

From the repository root, build one example with Go 1.25 or later:

```sh
GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags=-buildid= -o modules/wasm/triage/triage.wasm ./modules/wasm/triage
```

In an Undertow server or VPN client console, select an agent and run:

```text
use 1
run-wasm modules/wasm/triage/triage.wasm
run-wasm --background modules/wasm/triage/triage.wasm
job output JOB_ID
```

Run `run-wasm --stdin input.txt MODULE.wasm ARG1 ARG2` to pass a local input file and command-line arguments. The first guest argument is `undertow-module`, followed by the supplied arguments. The module's stdin, stdout, and stderr use standard WASI. Foreground stdout and stderr stream separately; a background job retains up to 256 KiB of combined output until it is removed. The agent's `wasm` capability gates the entire operation. `--deny=wasm` rejects all WASMs equally; `--deny=hostops`, `--deny=exec`, and other separate capabilities do not remove the WASM host imports. Anyone permitted to run WASM should be treated as having the agent process's read and outbound network privileges.

## Runtime and compatibility

The public host import module is **`undertow_host_v1`**. A module compiled against this namespace can expect its two function signatures and documented version 1 operations to remain compatible in future releases. New optional operations may be added within version 1; unknown operations return `-2`. An incompatible ABI will use a new namespace, such as `undertow_host_v2`, so old modules do not silently bind to changed signatures. Import resolution fails at instantiation if an agent predates the namespace. The `version` operation reports `{ "namespace": "undertow_host_v1", "major": 1 }`.

The current limits per run are:

| Resource | Limit |
| --- | ---: |
| Module transfer | 4 MiB |
| Supplied stdin | 64 KiB |
| Guest linear memory | 16 MiB |
| Combined stdout and stderr | 4 MiB |
| Wall-clock execution | 2 minutes |
| Simultaneous runs per agent | 2 |
| One host input or output | 64 KiB |
| One host operation | 15 seconds |
| Open socket handles per run | 8 |

The guest gets no preopened filesystem and no WASI network sockets. Host access is through explicit imports. A run ends when the module exits, the operator cancels the job or disconnects, the runtime limit expires, or an output limit is exceeded. Imports are synchronous. Host calls check the run context; filesystem traversal also checks it between entries. Individual network calls use a shorter timeout. The host API is intended for host assessment and bounded network interactions, not unbounded streaming.

## Exact ABI

All pointer and length values are unsigned 32-bit WebAssembly integers, referring to the module's own linear memory. `call` returns a signed 32-bit integer.

```c
// import module: undertow_host_v1
int32_t call(uint32_t op_ptr, uint32_t op_len,
             uint32_t input_ptr, uint32_t input_len,
             uint32_t output_ptr, uint32_t output_capacity);
int32_t last_error(uint32_t output_ptr, uint32_t output_capacity);
```

`op_ptr/op_len` is a UTF-8 operation name of 1–64 bytes, with no NUL terminator. `input_ptr/input_len` is a UTF-8 JSON object, or length zero for `{}`. `output_ptr/output_capacity` is a writable buffer. A successful call writes one compact UTF-8 JSON value without a NUL terminator and returns the byte count. Input and output may overlap; the host copies the input first. The caller owns all guest buffers and should keep them alive across the import call. Null pointers are valid only for zero-length input. Each nonzero memory span is bounds checked before use. The maximum JSON input and output capacity is 65,536 bytes. A returned string is a JSON string, so decode JSON before using its content.

Negative return codes are:

| Code | Meaning |
| ---: | --- |
| `-1` | Invalid length, pointer, or input JSON |
| `-2` | Unknown operation or unsupported platform operation |
| `-3` | Host operation failed, such as access denied, missing file, or network error |
| `-4` | Output does not fit the supplied buffer or the 64 KiB host value limit |

After an error, `last_error` copies the last call's human-readable UTF-8 error into a guest buffer and returns its byte count. Its capacity may be at most 4,096 bytes; it returns `-1` for an invalid buffer and `-4` if the message is too long. The message belongs to this module instance and is replaced by the next `call`. Modules should branch on the numeric code, not parse the message.

## Operations

All requests are JSON objects. Omitted numeric fields take the defaults below. Results shown as `text` are JSON strings containing platform-native command output. They are snapshots and may be truncated at 32 KiB by the agent's inventory command runner. An agent can return `-3` when an operating system command is unavailable or access is denied. Check `system.os` before assuming a platform-specific format.

| Group | Operation | Request fields | JSON result |
| --- | --- | --- | --- |
| Core | `version` | none | `{namespace,major}` |
| Core | `system` | none | `{os,arch,hostname,user,uid,gid,home,cwd,pid}`; IDs are strings |
| Identity | `environment` | optional `name` | With name: `{name,value,exists}`; without name: sorted `string[]` of `KEY=VALUE` |
| Identity | `privileges` | none | text: Windows `whoami /all`; Linux UID/GID, groups, capabilities, `NoNewPrivs` |
| Identity | `users`, `groups` | none | text: Windows `net user` / `net localgroup`; Linux `/etc/passwd` / `/etc/group` |
| Processes | `processes` | none | text: Windows `tasklist /fo csv`; Linux `ps -eo pid,ppid,user,comm` |
| Network inventory | `interfaces` | none | Array of `{name,index,mtu,flags,mac,addresses}` |
| Network inventory | `routes`, `dns_config` | none | text: native route table and DNS configuration |
| Network inventory | `neighbours`, `connections` | none | text: ARP/neighbour and connection/listener snapshots |
| Startup | `services`, `startup` | none | text: Windows service query and autoruns; Linux systemd units and unit files |
| Filesystem | `fs.list` | `path` | Array of `{name,directory,type}` immediate children, at most 512 |
| Filesystem | `fs.stat` | `path` | `{path,size,mode,permissions,directory,modified,symlink}` from `lstat` |
| Filesystem | `fs.read` | `path`, optional `offset`, `limit` | `{data_base64,bytes,offset}` |
| Filesystem | `fs.walk` | `path`, optional `depth` | Breadth-first array of stat objects, at most 256 |
| DNS | `dns.resolve` | `host` | Array of IP address strings |
| Outbound network | `net.tcp_exchange`, `net.udp_exchange` | `address`, optional `data`, `limit`, `timeout_ms` | `{connected,data_base64,bytes}` |
| Outbound network | `net.open` | `network` (`tcp` or `udp`), `address`, optional `timeout_ms` | `{handle}` |
| Outbound network | `net.write` | `handle`, `data`, optional `timeout_ms` | `{bytes}` |
| Outbound network | `net.read` | `handle`, optional `limit`, `timeout_ms` | `{data_base64,bytes,eof}` |
| Outbound network | `net.close` | `handle` | `{closed:true}` |
| Windows Registry | `registry.read` | `key`, optional `name` | text from `reg query` |
| Windows audit | `windows.service_names` | optional `offset`, `limit` | Sorted service registry subkey names; default page 128, maximum page 256 |
| Windows audit | `windows.service_config` | `name` | `{name,image_path,expanded_image_path,account,type,start}` from the service registry key |
| Windows audit | `windows.file_acl` | `path` | text from `icacls` for manual ACL review |

Filesystem paths resolve on the **agent**, relative to its working directory. They are not confined to the module's source directory. `fs.list` and `fs.stat` do not follow symlinks for metadata; `fs.walk` does not descend through symlinked directories. Listing results are bounded as shown above, and large JSON results can still return `-4`. For large trees, query narrower roots. `fs.walk` defaults to depth 2, caps depth at 5, errors if the root is inaccessible, and silently omits unreadable descendants. `fs.read` starts at a byte offset (default 0), returns raw bytes as standard base64, and reads at most 32 KiB per call (default 32 KiB). It can read any file the agent process can read. `permissions` is the numeric Go `FileMode.Perm()` value; Windows permissions are not a full ACL evaluation.

`dns.resolve` uses the agent's resolver and has a five-second timeout. The network operations require `address` in `host:port` form, or `[IPv6]:port`. `data` is standard base64 for up to 8 KiB of outbound bytes per call. Each read is capped at 8 KiB (`limit`, default 8 KiB). `timeout_ms` defaults to 5,000 and is capped at 5,000. A TCP exchange with no outbound data only tests connection establishment and returns immediately. A UDP exchange writes one datagram and waits for one reply; a silent service returns a timeout error. For multi-step protocols, use `net.open`, repeat `net.write` and `net.read`, then `net.close`. Handles are positive integers local to one module run, and the agent closes any remaining handles when the run ends. A missing or closed handle returns `-3`; opening a ninth concurrent handle returns `-3`. TCP reads may return fewer bytes than requested and must be repeated. A UDP read returns one datagram up to the requested limit. These operations use the agent's network reachability and privileges. They do not expose inbound listeners or arbitrary raw sockets.

`registry.read` is Windows only. `key` must start with `HKLM\\`, `HKCU\\`, `HKEY_LOCAL_MACHINE\\`, or `HKEY_CURRENT_USER\\`; `name` selects one value. A missing key, blocked access, or absent `reg.exe` is a host error. Other platforms return `-2`.

The `windows.*` audit operations are read-only and Windows-only. Page through `windows.service_names` with `offset` and `limit` until it returns fewer names than requested. `windows.service_config` accepts one service name and reads its current registry configuration. `expanded_image_path` resolves environment references with the agent process environment. `windows.file_acl` returns raw `icacls` output; it does not calculate effective permissions for the current token. A service path or ACL entry alone is not proof that the user can modify a service executable.

## Write and build a custom module

Go's WASI target offers a small way to use the ABI. From a clone of this repository, create `modules/wasm/my-audit/main.go`:

```go
package main

import (
    "fmt"
    "undertow/modules/wasm/hostapi"
)

func main() {
    var system struct { Hostname string `json:"hostname"`; OS string `json:"os"` }
    if err := hostapi.Call("system", nil, &system); err != nil { panic(err) }
    fmt.Printf("%s runs %s\n", system.Hostname, system.OS)
}
```

Build once on your development machine:

```sh
GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags=-buildid= -o my-audit.wasm ./modules/wasm/my-audit
```

The [Go wrapper source](../modules/wasm/hostapi/hostapi.go) shows the direct `//go:wasmimport` declarations and JSON buffer handling. A Rust module can import the same functions with `#[link(wasm_import_module = "undertow_host_v1")] extern "C"` and build for `wasm32-wasip1`; the two C signatures above are the language-neutral contract. Keep output buffers at or below 64 KiB and inspect negative returns. The `.wasm` artifact is portable across supported agent operating systems, though specific operations and examples can vary by platform.

For a local smoke test with an in-process Undertow agent, run:

```sh
UNDERTOW_WASM_TEST_MODULE=./my-audit.wasm go test ./internal/pivot -run '^TestCustomWASMOnAgent$' -v -count=1
```

On PowerShell, set `$env:UNDERTOW_WASM_TEST_MODULE = 'C:\\path\\to\\my-audit.wasm'` before the same `go test` command. This exercises the real agent mux and WASM runtime. A plain WASI runtime cannot supply Undertow imports unless it implements this namespace. To run on a remote agent, use `run-wasm my-audit.wasm` in the selected-agent console. See [console commands](cli-reference.md) for server and client invocation.
