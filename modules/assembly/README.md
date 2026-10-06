# Packaged .NET Framework assemblies

The console preloads managed `.exe` and `.dll` files in this directory as
`assembly-NAME` commands. Each shipped example has a sidecar with its normal
usage and attribution. These are Windows amd64/.NET Framework examples and
have not been live-tested as part of this package; run `help assembly-NAME`
before using one.

| Command | File | Source and purpose |
| --- | --- | --- |
| `assembly-certify` | `Certify.exe` | [GhostPack/Certify](https://github.com/GhostPack/Certify), AD CS enumeration and abuse research tool (3-clause BSD). |
| `assembly-rubeus` | `Rubeus.exe` | [GhostPack/Rubeus](https://github.com/GhostPack/Rubeus), Kerberos interaction and auditing tool (3-clause BSD). |
| `assembly-seatbelt` | `Seatbelt.exe` | [GhostPack/Seatbelt](https://github.com/GhostPack/Seatbelt), host security enumeration tool (3-clause BSD; includes the upstream NOTICE/license terms). |

Put additional managed files here with `FILE.exe.json` or `FILE.json` for
description, usage, help, and source attribution. See
[assembly modules](../../docs/assembly-modules.md) and the [local module bank](../../docs/module-bank.md).
