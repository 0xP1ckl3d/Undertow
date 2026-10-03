# BOF compatibility

Undertow can run a compatible, already compiled Beacon Object File (BOF) directly as an AMD64 COFF `.o` file. This complements portable [WASM modules](wasm-development.md) and Undertow [native `.module` DLLs](native-modules.md). New Undertow native modules use `undertow_native_v1` and `run-native`; existing BOFs use the conventional `go` and Beacon ABI with `run-bof`. No `nativepack` conversion is needed.

For repeated use, put the `.o` and its optional help/argument sidecar in the local [module bank](module-bank.md). The console preloads packaged BOFs as commands such as `bof-hello` and `bof-arguments`. You can also use `load bof FILE [NAME] [--format FORMAT]` during a session.

## From object file to an agent run

If you already have a compatible Windows AMD64 BOF, inspect it on the **console host**, then put the `.o` and optional `.json` argument sidecar under the local `modules/` bank. Start or reattach the console so it loads the command; no BOF file needs to be installed on the agent.

```sh
undertow bof inspect modules/bof/hello.o
```

```text
modules
help bof-hello
use 1
bof-hello
```

To build the packaged examples from source, run `./modules/bof/build.ps1` in an x64 MSVC Developer PowerShell before opening the console. For a one-off file run, use `run-bof modules/bof/hello.o` after selecting an agent. The [module bank](module-bank.md) explains naming and sidecars; the sections below describe supported COFF features and typed arguments.

## Supported subset

The first target is Windows amd64 agents and standard x64 COFF object files with a defined `go` symbol. The loader parses the COFF header, sections, symbols and string table, checks all offsets and relocation records, then allocates per-run sections and resolves external symbols. It supports `IMAGE_REL_AMD64_ADDR64`, `ADDR32`, `ADDR32NB`, `REL32`, `REL32_1` through `REL32_5`, `SECTION`, and `SECREL`. Unsupported relocations, architecture, malformed records, missing entry points, and unknown external symbols produce descriptive errors before invoking `go`. The current subset does not include COMDAT selection, weak external resolution, relocation overflow records, companion object linking, C++ runtime initialization, or arbitrary COFF objects.

Windows imports use the usual `LIBRARY$Export` spelling, including `__imp_LIBRARY$Export` for indirect imports. For example, `ADVAPI32$OpenProcessToken` resolves `OpenProcessToken` from `advapi32.dll`. The library name is not limited to a fixed list of Windows APIs. Standard plain imports of `GetModuleHandleA`, `LoadLibraryA`, `GetProcAddress` and `FreeLibrary`, including their `__imp_` forms, resolve through `kernel32.dll`. Other plain `__imp_FunctionName` imports are unsupported because COFF does not identify their DLL; use `__imp_LIBRARY$Export` or `LIBRARY$Export`. The loader searches System32 for each imported DLL and reports the original symbol when a library or export cannot be resolved. DLL handles and loaded BOF memory belong to one run.

The Beacon compatibility bridge currently exports `BeaconPrintf`, `BeaconOutput`, `BeaconDataParse`, `BeaconDataPtr`, `BeaconDataInt`, `BeaconDataShort`, `BeaconDataExtract`, `BeaconDataLength`, `BeaconFormatAlloc`, `BeaconFormatReset`, `BeaconFormatFree`, `BeaconFormatAppend`, `BeaconFormatPrintf`, `BeaconFormatToString`, and `BeaconFormatInt`. Beacon output uses the same foreground stream and retained job output as other agent work. Unsupported `Beacon*` imports are reported by name. The bridge and `go` entry point use the Windows x64 calling convention; `go` has a void return type, so a normal return has status 0.

## Inspect and run

```text
undertow bof inspect modules/bof/hello.o

undertow> use 1
undertow[TALON]> run-bof modules/bof/hello.o
undertow[TALON]> run-bof modules/bof/arguments.o --format "iszZb" 123 7 hello "C:\Program Files" @payload.bin
undertow[TALON]> run-bof --background modules/bof/loop.o
undertow[TALON]> jobs
undertow[TALON]> job output 1
undertow[TALON]> job stop 1
```

At the main console level, use `run-bof AGENT_ID OBJECT.o`. `bof inspect FILE.o` runs locally and reports architecture, entry point, section/symbol/relocation counts, Windows and Beacon imports, unresolved symbols, and all compatibility reasons found in one parse. Inspection and execution use the same parser and compatibility checks. Inspection cannot confirm that the selected agent has a particular DLL or exported function; that is resolved on the agent.

`--format` accepts one character per argument: `i` signed 32-bit integer, `s` signed 16-bit integer, `z` NUL-terminated ANSI byte string, `Z` NUL-terminated UTF-16LE wide string, `b` binary bytes. Integer values use decimal or `0x` notation. A binary value is `@LOCAL_FILE` or `base64:DATA`. `z` passes UTF-8 bytes without a locale conversion; BOFs expecting a legacy Windows code page should use suitable input. The argument packet has a big-endian 32-bit payload length, followed by values in format order. Strings and binary values each have a big-endian 32-bit byte length; integers are big-endian. The BOF receives the packet pointer and byte length in `go(char *args, int len)` and can parse it through `BeaconData*`. An empty format passes a four-byte zero length header. Argument input is limited to 1 MiB.

For frequently used objects, `OBJECT.o.json` or `OBJECT.json` may supply command metadata:

```json
{
  "name": "example",
  "description": "Example BOF",
  "usage": "example <pid> <target>",
  "help": "Longer operator help is optional.",
  "entrypoint": "go",
  "arguments": [
    {"name": "pid", "type": "int", "required": true},
    {"name": "target", "type": "wstring", "required": true}
  ]
}
```

Use `run-bof example.o 123 "C:\Program Files"` when the sidecar exists, or select another file with `--manifest FILE`. Accepted types are `int`/`int32`, `short`/`int16`, `string`/`ansi`, `wstring`/`wide`, and `binary`. An explicit `--format` overrides the manifest's argument list. The `.o` file itself never needs a sidecar.

## Loaded console commands

Load a BOF once to use it as a command in the current operator or client console:

```text
undertow> load bof /path/to/example.o example
Loaded BOF: example
undertow> help example
undertow> bofs
undertow> use 1
undertow[TALON]> example 123 "C:\Program Files"
undertow[TALON]> example 123 "C:\Program Files" --background
undertow[TALON]> back
undertow> use 2
undertow[OTHERHOST]> example 456 "C:\Windows"
undertow> unload bof example
```

`load bof FILE [NAME]` validates the COFF object immediately and stores its bytes locally in the console process. If NAME is omitted, the command name comes from the filename; `example.x64.o` becomes `example`. A loaded alias cannot shadow a built-in command or another loaded BOF. `bofs` lists the aliases, paths and argument types. General `help` includes a Loaded BOFs section, and `help NAME` displays the manifest's description, usage, arguments, longer help, source and architecture. Tab completes loaded aliases, help topics and unload targets.

The sidecar supplies argument types, so invocations do not need `--format`. Existing sidecars remain valid; `description`, `usage`, `help` and `required` are optional fields. Omitted `required` means true; optional arguments must come after required ones. With no sidecar, register typed arguments once with `load bof example.o example --format "zi"`: this expects an ANSI string followed by a signed 32-bit integer. With neither a sidecar nor `--format`, the schema is shown as unspecified and all supplied arguments are encoded as ANSI strings. Undertow does not infer types from COFF code. Explicit argument counts and types are checked locally before transfer.

The registration is local to one console process. It is independent of agent selection and survives switching agents, reconnects and background jobs. The object is transferred only when invoked, and execution still uses the `run-bof` runtime and normal job system. The registration is removed by `unload bof NAME` or when that console process exits; there is no server-side BOF library or restart persistence. `run-bof` remains available for one-off runs and troubleshooting.

## Build and runtime behavior

The checked-in examples use MSVC x64. In an x64 Developer PowerShell:

```powershell
./modules/bof/build.ps1
undertow bof inspect modules/bof/hello.o
```

The build uses `cl.exe /c /GS- /Zl /O1`; it produces raw COFF `.o` files directly. Include only declarations for Beacon helpers and Windows imports expected by the BOF environment. See [the example source and build notes](../modules/bof/README.md). Existing compatible BOFs built with another conventional toolchain can be inspected and used without rebuilding.

BOFs run under the agent's `native` capability, two-run concurrency limit, execution deadline and output limit. Background runs use the existing jobs manager and its retained output. The agent launches a child process per BOF so cancellation, `job stop`, deadline expiry or a crashing BOF can terminate that run. The child owns loaded sections, argument storage, imported DLL handles and formatting allocations. The agent and server retain only the normal job state and bounded output. A BOF can call normal Windows APIs with the privileges of the agent account. The Beacon ABI has no cancellation callback, so BOFs that do not return are stopped by terminating their worker process.

## Troubleshooting

- `unsupported COFF architecture`: use a Windows AMD64 BOF, not an ARM64 or x86 object.
- `missing go entry point`: export a COFF symbol named `go`.
- `unsupported AMD64 COFF relocation`: the object uses a relocation outside the listed subset.
- `unsupported Beacon API import`: the object needs a Beacon helper not yet implemented.
- `unresolved Windows import`: check the `LIBRARY$Export` spelling and availability of that DLL/export on the agent.
- `BOF argument count does not match --format`: provide one value per format character or use a matching sidecar.

`run-native`, UTN1, `nativepack` and `undertow_native_v1` remain the development path for Undertow-specific native modules. BOF compatibility is an independent loader and bridge that shares transport, capability, jobs and console output.

Back to [documentation home](README.md).
