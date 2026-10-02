# Native Windows examples

Run `examples/native/build.ps1` from an **x64 MSVC Developer PowerShell**. The script compiles `hello.c`, `wininfo.c`, and `hostcheck.c` as x64 DLLs, wraps each with `tools/nativepack`, and leaves ready to run `.module` files. Run `examples/native/build.ps1 hostcheck` (or another example name) to build one. Go 1.25 or newer and Visual Studio 2022 C++ tools with the Windows SDK are required for the reference build. The checked-in `.module` files were built with this script.

```text
use 1
run-native examples/native/hello/hello.module one "two words"
run-native examples/native/wininfo/wininfo.module
run-native examples/native/hostcheck/hostcheck.module
run-native --background examples/native/hello/hello.module --wait
jobs
job stop 1
```

`hello` shows arguments, opaque data length, output, exit status (`--fail` returns 7), and cooperative cancellation (`--wait`). `wininfo` calls `GetComputerNameW`, `GetNativeSystemInfo`, `GetCurrentProcessId`, and `GlobalMemoryStatusEx` directly from Windows SDK headers and reports the result through the Undertow output API.
`hostcheck` reports the Windows computer, user, process, token elevation, integrity level, privileges, and active network adapters through standard Windows APIs. It only reads host state.

`sift/` contains a Windows AMD64 native sensitive-data scanner built from the pinned Stratus Sift rule catalogue. It reports matched values and locations from local files or accessible SMB shares. Build it with `examples/native/sift/build.ps1`; see its README for scope and license details.
