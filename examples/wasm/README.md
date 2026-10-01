# Packaged WASM host-assessment modules

Each directory contains readable Go source and a compiled WASI `.wasm`. All modules use the same public [`undertow_host_v1` API](../../docs/wasm-development.md) available to any custom or third-party WASM. They execute with the agent process's host privileges. These examples report observations; they do not change host configuration.

Build from the repository root with Go 1.25 or later:

```sh
sh examples/wasm/build.sh
# Or on PowerShell:
./examples/wasm/build.ps1
```

The script uses `GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags=-buildid=` for each package. To build one module, pass its directory name to the script. Select an agent in either Undertow console with `use 1`, then use a command below. Paths refer to the **operator's** local `.wasm` files. Each module's filesystem and network queries run on the **agent**.

| Module | Purpose | Example command | Representative output (host dependent) |
| --- | --- | --- | --- |
| [`triage`](triage/main.go) | OS, architecture, user and privilege context | `run-wasm examples/wasm/triage/triage.wasm` | `Host triage: workstation (windows/amd64)`<br>`User: EXAMPLE\\analyst uid=... gid=...` |
| [`privilege-audit`](privilege-audit/main.go) | Privilege context and writable system path checks | `run-wasm examples/wasm/privilege-audit/privilege-audit.wasm` | `Privilege and configuration audit: workstation`<br>`C:\\ProgramData mode=d--------- size=0` |
| [`inventory`](inventory/main.go) | Processes, services, connections and neighbours | `run-wasm examples/wasm/inventory/inventory.wasm tcp` | `Process, service and network inventory`<br>`[connections]`<br>`TCP 127.0.0.1:... LISTENING ...` |
| [`artifact-discovery`](artifact-discovery/main.go) | Interesting file names and metadata under a chosen root | `run-wasm examples/wasm/artifact-discovery/artifact-discovery.wasm` | `Interesting file names under C:\\Users\\analyst (metadata only):`<br>`...\\.ssh mode=d--------- size=0` |
| [`persistence-audit`](persistence-audit/main.go) | Service startup and user autoruns | `run-wasm examples/wasm/persistence-audit/persistence-audit.wasm` | `Startup and autorun audit: workstation`<br>`SERVICE_NAME: ExampleService` |
| [`enterprise-posture`](enterprise-posture/main.go) | Domain environment, DNS and OS policy locations | `run-wasm examples/wasm/enterprise-posture/enterprise-posture.wasm` | `Enterprise posture: workstation (windows)`<br>`USERDOMAIN=EXAMPLE` |

`inventory` accepts an optional case-insensitive line filter argument. `artifact-discovery` accepts an optional agent-side root path; its default is the agent user's home directory. It also reads extra file-name patterns, one per line, from WASI stdin. For example:

```text
run-wasm --stdin ./patterns.txt examples/wasm/artifact-discovery/artifact-discovery.wasm C:\Users\analyst
```

To retain a run as a job:

```text
run-wasm --background examples/wasm/triage/triage.wasm
job output JOB_ID
```

The compiled modules are checked in because the repository's ignore policy permits `.wasm`. The source remains the source of truth. Rebuild with the script after editing source. For extension and local testing of your own modules, read the [developer guide](../../docs/wasm-development.md).
