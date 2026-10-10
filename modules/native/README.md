# Native Windows examples

See the [module bank](../../docs/module-bank.md) for every shipped module and its console command.

Run `modules/native/build.ps1` from an **x64 MSVC Developer PowerShell**. The script compiles `hello`, `wininfo`, `hostcheck`, and the C++ `shellpower` adapter as x64 DLLs, wraps each with `tools/nativepack`, and leaves ready to run `.module` files. Run `modules/native/build.ps1 shellpower` (or another module name) to build one. Go 1.25 or newer and Visual Studio 2022 C++ tools with the Windows SDK are required for the reference build. The checked-in `.module` files were built with this script.

```text
use 1
run-native modules/native/hello/hello.module one "two words"
run-native modules/native/wininfo/wininfo.module
run-native modules/native/hostcheck/hostcheck.module
run-native modules/native/shellpower/shellpower.module 'Get-Process | Select-Object -First 5'
run-native --data ./audit.ps1 modules/native/shellpower/shellpower.module
run-native --background modules/native/hello/hello.module --wait
jobs
job stop 1
```

`hello` shows arguments, opaque data length, output, exit status (`--fail` returns 7), and cooperative cancellation (`--wait`). `wininfo` calls `GetComputerNameW`, `GetNativeSystemInfo`, `GetCurrentProcessId`, and `GlobalMemoryStatusEx` directly from Windows SDK headers and reports the result through the Undertow output API.
`hostcheck` reports the Windows computer, user, process, token elevation, integrity level, privileges, and active network adapters through standard Windows APIs. It only reads host state.
`shellpower` adapts the separate ShellPower project to run an inline Windows PowerShell command or an uploaded `.ps1` inside the agent process. The same packaged module supplies a persistent **ShellPower · in process** choice in the GUI's Live shell tab. Its original CLR host and patching behavior are preserved; Undertow supplies argument decoding, terminal input and output routing, AppDomain cleanup, and module packaging. See its [module guide](shellpower/README.md).

`sift/` contains a Windows AMD64 native sensitive-data scanner built from the pinned Stratus Sift rule catalogue. It reports matched values and locations from local files or accessible SMB shares. Build it with `modules/native/sift/build.ps1`; see its README for scope and license details.

Back to [documentation home](../../docs/README.md).
