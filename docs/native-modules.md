# Developing Undertow native modules

To run an existing tool, follow [GUI Modules](gui.md#run-tools-from-modules) and the [module bank](module-bank.md). This guide explains how to build and validate a `.module` artifact, with terminal commands for development.

Native modules complement [WASM modules](wasm-development.md). WASM uses portable `wasip1/wasm` code and `undertow_host_v1` imports. Native modules target a specific OS and architecture, use ordinary platform APIs, and use `undertow_native_v1` only for Undertow output, arguments, cancellation, and run information. [BOF compatibility](bof-compatibility.md) is a third extension path for existing AMD64 COFF `.o` files with the Beacon ABI; those run with `run-bof`, while Undertow `.module` DLLs run with `run-native`. Native code has the agent process's privileges and shares its address space; use modules you trust. A native crash can terminate the agent process.

For repeated use, place a `.module` file and optional help sidecar in the local [module bank](module-bank.md). The console preloads packaged native modules as commands such as `module-wininfo`; `load module FILE [NAME]` registers one during a session.

## From source to an agent run

Build a native example on a Windows development machine with an x64 MSVC Developer PowerShell, then put the resulting `.module` and optional `.json` sidecar on the **console host** under `modules/`. Start or reattach the console so it scans the bank; the agent does not need the file installed locally.

```powershell
./modules/native/build.ps1
```

```text
modules
help module-wininfo
use 1
module-wininfo
```

`modules/native/build.ps1` creates the packaged examples in their module directories, so a console started from this repository can preload them directly. Use `run-native modules/native/wininfo/wininfo.module` for a one-off run. See [module bank placement and naming](module-bank.md) and the [console guide](console.md#running-an-agent-program-and-transferring-files) for selection, output, and jobs.

## Supported format and build

The initial target is **Windows amd64**. The payload is an ordinary PE32+ x64 DLL. Undertow wraps it in a small `.module` container with explicit metadata. The format is `UTN1`, a little-endian `uint32` JSON metadata length (at most 4096), compact UTF-8 JSON metadata, then the complete DLL bytes. Metadata contains `name`, `version`, `runtime: "native"`, `os: "windows"`, `arch: "amd64"`, `abi: "undertow_native_v1"`, and optional `description`. The container size is at most 8 MiB. The same metadata fields leave room for Windows arm64 and Linux variants in the operator command without changing `run-native`.

Use an x64 MSVC Developer PowerShell with the Windows SDK and Go 1.25 or newer:

```powershell
./modules/native/build.ps1
```

The script compiles with `cl.exe /LD /MT /O2 /W4`, links `kernel32.lib`, then invokes `go run ./tools/nativepack` to produce the `.module` file. It removes intermediate DLL, object, export, and import-library files. A custom module can use the same pattern:

```powershell
cl.exe /nologo /LD /MT /O2 /W4 /I sdk/native mymodule.c /link /OUT:mymodule.dll kernel32.lib
go run ./tools/nativepack -dll mymodule.dll -out mymodule.module -name mymodule -version 1.0.0
```

MSVC x64 is the reference toolchain. `/MT` statically links the C runtime, avoiding a deployment requirement for the Visual C++ redistributable. C++ authors must export `undertow_main` with C linkage. Other compilers may produce compatible PE DLLs but are outside the initial tested matrix. The module can link standard Windows import libraries appropriate to its functions.

## Entry point and ABI

Include [the SDK header](../sdk/native/undertow_native.h):

```c
#include <windows.h>
#include "undertow_native.h"

int32_t undertow_main(const undertow_native_api_v1 *api,
                      const void *args, size_t args_len) {
    DWORD pid = GetCurrentProcessId();
    if (api->version != UNDERTOW_NATIVE_ABI_VERSION) return 2;
    undertow_native_printf(api, "pid=%lu\n", (unsigned long)pid);
    return 0;
}
```

The exported name must be exactly `undertow_main`. On Windows x64 it uses the platform's x64 calling convention. The API is a C POD table with `size`, `version`, an opaque integer `context`, function pointers for stdout, stderr, cancellation and job state, plus OS, architecture and agent process ID. The runtime passes no Go pointer or object. API and argument memory are native allocations, borrowed until the entry point returns. Module-owned memory stays with the module; allocate it with your C runtime or Windows APIs. Join module-created threads before returning, because Undertow unloads the DLL when the entry point returns. Never retain API pointers after return. `write` and `write_error` accept bytes with explicit lengths and return zero or `-1`. `undertow_native_printf` is a bounded formatting helper in the header. A write call is limited to 32 KiB. Background output spills from memory to a server file after 256 KiB and is bounded by the server's job output limits; there is no native two-minute or 4 MiB cap.

Arguments are deterministic and binary safe: little-endian `uint32 argc`, then for each argument `uint32 length` plus UTF-8 bytes, then `uint32 length` plus opaque bytes from `--data`. The header supplies checked `undertow_native_arg` and `undertow_native_data` helpers. Strings are length-delimited rather than NUL-terminated. At most 256 arguments and 64 KiB of opaque data are allowed; the entire encoded buffer is at most 128 KiB.

## Running and jobs

```text
use 1
run-native modules/native/hello/hello.module one "two words"
run-native --data payload.bin modules/native/hello/hello.module
run-native --background modules/native/hello/hello.module --wait
jobs
job output 1
job stop 1
```

At the main console level, put an agent ID after `run-native`. The local console reads the `.module` and optional data file; the server relays bytes to the selected agent. The agent validates metadata and PE headers, loads the DLL, finds `undertow_main`, invokes it, streams output, and unloads the DLL after return. The independent `native` capability gates both foreground and background runs (`--deny=native`). The runtime allows two simultaneous runs without a fixed two-minute deadline. Background output stays in server memory through 256 KiB, then spills the complete stream to the server job output directory; use `job save` to download it. `job stop` closes the session and raises `api->cancelled(...)` / `api->job_state(...)`. Cancellation is cooperative: a DLL that never returns cannot be safely unloaded from within the agent process, so modules must check cancellation during long operations. Agent disconnect also cancels the run context.

## Windows imports and loading errors

The runtime writes the DLL to a private temporary directory, calls Windows `LoadLibraryEx` with a System32-only dependency search, then calls `GetProcAddress`. Windows resolves normal PE imports, base relocations, TLS initialization and DLL entry processing. A module can use normal Windows SDK declarations and import libraries for DLLs available in System32. V1 does not load companion DLLs from the module package, arbitrary directories, or an Undertow wrapper library. For libraries outside System32, the author must use Windows' normal explicit loading APIs with their own deployment strategy. The loader accepts x64 PE base relocation records of type `IMAGE_REL_BASED_DIR64`, plus `IMAGE_REL_BASED_ABSOLUTE` padding; it rejects other types with an explicit error before Windows loading. A relocation directory is required so repeated and concurrent DLL loads can use different bases. Undertow does not implement a COFF object linker or claim arbitrary COFF relocation compatibility. Build a relocatable x64 DLL with the reference MSVC options. Missing imports or other Windows image problems surface from `LoadLibraryEx` with the Windows loader error preserved.

Useful errors include `invalid native module container`, `unsupported module architecture`, `unsupported native ABI`, `missing native entry point undertow_main`, `load native module`, `native module returned non-zero status`, and `native module: context canceled`. A malformed container fails before execution. Unsupported metadata OS, architecture and ABI fail on the agent before loading. A missing DLL import fails during `LoadLibraryEx`; an absent exported entry fails during `GetProcAddress`. The current compatibility subset is Windows x64 PE32+ DLLs built with MSVC x64 `/LD /MT`, standard System32 DLL imports, and a single exported entry point.

See [the examples](../modules/native/README.md) for `hello`, `wininfo`, and `hostcheck`. The latter two demonstrate direct Windows APIs rather than extra Undertow host operations.

Back to [documentation home](README.md).
