# BOF compatibility examples

See the [module bank](../../docs/module-bank.md) for every shipped module and its console command.

Run `./modules/bof/build.ps1` in an x64 MSVC Developer PowerShell. It builds raw AMD64 COFF `.o` files with `cl.exe /c /GS- /Zl /O1 /W4`; no wrapping or post-processing is needed. The compiled objects are included for inspection and tests. `beacon.h` contains the minimal conventional Beacon declarations used by these examples.

- `hello.c` exercises `BeaconPrintf`, `BeaconOutput`, a `KERNEL32$` import, and multiple sections.
- `arguments.c` exercises integer, short, ANSI, wide and binary argument parsing plus the `BeaconFormat*` helpers. Its optional `arguments.o.json` sidecar supplies the format and console help. Try `load bof modules/bof/arguments.o`, then `arguments 123 7 hello world base64:AAEC` on a selected agent.
- `imports.c` exercises imports from kernel32, advapi32, netapi32 and iphlpapi.
- `loop.c` is a long-running object for cancellation and job tests.
- `loaderimports.c` exercises standard `__imp_` runtime loader imports, direct and indirect `DLL$Export` imports, and `BeaconDataPtr`. Run it with `--format i 0x01020304`.
- `internal/bof/testdata/unsupported_imports.c` is an intentional negative fixture for inspection. It lives outside the startup module bank because it cannot execute.

Inspect a compiled object with `undertow bof inspect modules/bof/hello.o`, then select a Windows amd64 agent and run `run-bof modules/bof/hello.o`. See [the compatibility guide](../../docs/bof-compatibility.md) for the supported COFF subset and argument packet.

Back to [documentation home](../../docs/README.md).
