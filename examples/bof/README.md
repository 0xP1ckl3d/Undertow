# BOF compatibility examples

Run `./examples/bof/build.ps1` in an x64 MSVC Developer PowerShell. It builds raw AMD64 COFF `.o` files with `cl.exe /c /GS- /Zl /O1 /W4`; no wrapping or post-processing is needed. The compiled objects are included for inspection and tests. `beacon.h` contains the minimal conventional Beacon declarations used by these examples.

- `hello.c` exercises `BeaconPrintf`, `BeaconOutput`, a `KERNEL32$` import, and multiple sections.
- `arguments.c` exercises integer, short, ANSI, wide and binary argument parsing plus the `BeaconFormat*` helpers. Its optional `arguments.o.json` sidecar supplies the format. Try `run-bof examples/bof/arguments.o 123 7 hello world base64:AAEC`.
- `imports.c` exercises imports from kernel32, advapi32, netapi32 and iphlpapi.
- `loop.c` is a long-running object for cancellation and job tests.

Inspect a compiled object with `undertow bof inspect examples/bof/hello.o`, then select a Windows amd64 agent and run `run-bof examples/bof/hello.o`. See [the compatibility guide](../../docs/bof-compatibility.md) for the supported COFF subset and argument packet.
