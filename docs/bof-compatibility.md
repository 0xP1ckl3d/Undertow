# BOF compatibility

Undertow can run a compatible, already compiled Beacon Object File (BOF) directly as an AMD64 COFF `.o` file. This complements portable [WASM modules](wasm-development.md) and Undertow [native `.module` DLLs](native-modules.md). New Undertow native modules use `undertow_native_v1` and `run-native`; existing BOFs use the conventional `go` and Beacon ABI with `run-bof`. No `nativepack` conversion is needed.

## Supported subset

The first target is Windows amd64 agents and standard x64 COFF object files with a defined `go` symbol. The loader parses the COFF header, sections, symbols and string table, checks all offsets and relocation records, then allocates per-run sections and resolves external symbols. It supports `IMAGE_REL_AMD64_ADDR64`, `ADDR32`, `ADDR32NB`, `REL32`, `REL32_1` through `REL32_5`, `SECTION`, and `SECREL`. Unsupported relocations, architecture, malformed records, missing entry points, and unknown external symbols produce descriptive errors before invoking `go`. The current subset does not include COMDAT selection, weak external resolution, relocation overflow records, companion object linking, C++ runtime initialization, or arbitrary COFF objects.

Windows imports use the usual `LIBRARY$Export` spelling, including `__imp_LIBRARY$Export` for indirect imports. For example, `ADVAPI32$OpenProcessToken` resolves `OpenProcessToken` from `advapi32.dll`. The library name is not limited to a fixed list of Windows APIs. The loader searches System32 for each imported DLL and reports the original symbol when a library or export cannot be resolved. DLL handles and loaded BOF memory belong to one run.

The Beacon compatibility bridge currently exports `BeaconPrintf`, `BeaconOutput`, `BeaconDataParse`, `BeaconDataInt`, `BeaconDataShort`, `BeaconDataExtract`, `BeaconDataLength`, `BeaconFormatAlloc`, `BeaconFormatReset`, `BeaconFormatFree`, `BeaconFormatAppend`, `BeaconFormatPrintf`, `BeaconFormatToString`, and `BeaconFormatInt`. Beacon output uses the same foreground stream and retained job output as other agent work. Unsupported `Beacon*` imports are reported by name. The bridge and `go` entry point use the Windows x64 calling convention; `go` has a void return type, so a normal return has status 0.

## Inspect and run

```text
undertow bof inspect examples/bof/hello.o

undertow> use 1
undertow[TALON]> run-bof examples/bof/hello.o
undertow[TALON]> run-bof examples/bof/arguments.o --format "iszZb" 123 7 hello "C:\Program Files" @payload.bin
undertow[TALON]> run-bof --background examples/bof/loop.o
undertow[TALON]> jobs
undertow[TALON]> job output 1
undertow[TALON]> job stop 1
```

At the main console level, use `run-bof AGENT_ID OBJECT.o`. `bof inspect FILE.o` runs locally and reports architecture, entry point, section/symbol/relocation counts, Windows and Beacon imports, and compatibility reasons. Inspection and execution use the same parser and compatibility checks. Inspection cannot confirm that the selected agent has a particular DLL or exported function; that is resolved on the agent.

`--format` accepts one character per argument: `i` signed 32-bit integer, `s` signed 16-bit integer, `z` NUL-terminated ANSI byte string, `Z` NUL-terminated UTF-16LE wide string, `b` binary bytes. Integer values use decimal or `0x` notation. A binary value is `@LOCAL_FILE` or `base64:DATA`. `z` passes UTF-8 bytes without a locale conversion; BOFs expecting a legacy Windows code page should use suitable input. The argument packet has a big-endian 32-bit payload length, followed by values in format order. Strings and binary values each have a big-endian 32-bit byte length; integers are big-endian. The BOF receives the packet pointer and byte length in `go(char *args, int len)` and can parse it through `BeaconData*`. An empty format passes a four-byte zero length header. Argument input is limited to 1 MiB.

For frequently used objects, `OBJECT.o.json` or `OBJECT.json` may supply convenience metadata:

```json
{
  "name": "example",
  "entrypoint": "go",
  "arguments": [
    {"name": "pid", "type": "int"},
    {"name": "target", "type": "wstring"}
  ]
}
```

Use `run-bof example.o 123 "C:\Program Files"` when the sidecar exists, or select another file with `--manifest FILE`. Accepted types are `int`/`int32`, `short`/`int16`, `string`/`ansi`, `wstring`/`wide`, and `binary`. An explicit `--format` overrides the manifest's argument list. The `.o` file itself never needs a sidecar.

## Build and runtime behavior

The checked-in examples use MSVC x64. In an x64 Developer PowerShell:

```powershell
./examples/bof/build.ps1
undertow bof inspect examples/bof/hello.o
```

The build uses `cl.exe /c /GS- /Zl /O1`; it produces raw COFF `.o` files directly. Include only declarations for Beacon helpers and Windows imports expected by the BOF environment. See [the example source and build notes](../examples/bof/README.md). Existing compatible BOFs built with another conventional toolchain can be inspected and used without rebuilding.

BOFs run under the agent's `native` capability, two-run concurrency limit, execution deadline and output limit. Background runs use the existing jobs manager and its retained output. The agent launches a child process per BOF so cancellation, `job stop`, deadline expiry or a crashing BOF can terminate that run. The child owns loaded sections, argument storage, imported DLL handles and formatting allocations. The agent and server retain only the normal job state and bounded output. A BOF can call normal Windows APIs with the privileges of the agent account. The Beacon ABI has no cancellation callback, so BOFs that do not return are stopped by terminating their worker process.

## Troubleshooting

- `unsupported COFF architecture`: use a Windows AMD64 BOF, not an ARM64 or x86 object.
- `missing go entry point`: export a COFF symbol named `go`.
- `unsupported AMD64 COFF relocation`: the object uses a relocation outside the listed subset.
- `unsupported Beacon API import`: the object needs a Beacon helper not yet implemented.
- `unresolved Windows import`: check the `LIBRARY$Export` spelling and availability of that DLL/export on the agent.
- `BOF argument count does not match --format`: provide one value per format character or use a matching sidecar.

`run-native`, UTN1, `nativepack` and `undertow_native_v1` remain the development path for Undertow-specific native modules. BOF compatibility is an independent loader and bridge that shares transport, capability, jobs and console output.
