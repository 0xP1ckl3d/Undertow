# Privilege and configuration audit

See the [module bank](../../../docs/module-bank.md) for every shipped module and its console command.

A read-only WASM example inspired by [PrivescCheck](https://github.com/itm4n/PrivescCheck). It runs on the selected Undertow agent through `undertow_host_v1`, with no PowerShell script on the agent.

Build from the repository root with `./modules/wasm/build.ps1 privilege-audit` or `sh modules/wasm/build.sh privilege-audit`, then run:

```text
use 1
run-wasm modules/wasm/privilege-audit/privilege-audit.wasm
```

Implemented Windows checks:

| Check | Output | Basis |
| --- | --- | --- |
| AlwaysInstallElevated | Finding only when both HKLM and HKCU equal 1 | Registry values |
| Automatic logon, LSA protection, WDigest, UAC and WSUS transport | Review or context | Registry values; no password value is printed |
| Selected token privileges | Context | `whoami /all` snapshot |
| Unquoted SYSTEM service paths | Review candidates with possible executable prefixes and parent ACL text | Service registry and `icacls` |

The service path check does **not** claim exploitability. Effective write access, an existing prefix executable, service start permissions, and restart conditions require further assessment. The module does not evaluate Windows ACLs, extract credentials, run payloads, or change the target. Unavailable registry values are omitted.

This covers a subset of PrivescCheck's Windows checks. PrivescCheck also checks scheduled tasks, applications, credentials, network settings, hardening, updates, and more; those are not replicated here. On Linux, the example retains its earlier UID/GID and world-writable system path checks. Other operating systems print that no checks are implemented.

See the [host API guide](../../../docs/wasm-development.md) for API details and limits.

Back to [documentation home](../../../docs/README.md).
