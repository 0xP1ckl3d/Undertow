# Local module bank

Use the module bank when you have a compiled tool and want to run it by name in the **server or VPN client console**. Undertow loads packaged BOFs, native modules, and WASM modules from `modules/` when that console process starts. These are local, session-scoped commands. Loading reads each artifact into the console process; Undertow sends its bytes to the selected agent only when you run the command.

| I want to... | Start here |
| --- | --- |
| Run a packaged tool | In the console, type `modules`, `help NAME`, `use NUMBER`, then `NAME` as shown below. |
| Add a tool I already compiled | Put its artifact and optional JSON sidecar on the **console host** under `modules/`, then start or reattach the console. |
| Build a new tool | Follow the [WASM](wasm-development.md), [native Windows module](native-modules.md), or [BOF](bof-compatibility.md) guide, then add the resulting file here. |

The repository ships runnable files in `modules/bof/`, `modules/native/`, and `modules/wasm/`. To add your own, copy a compatible `.o`, `.module`, or `.wasm` file anywhere below `modules/`, then start or reattach a console. Subdirectories are for organization only; the file extension determines the runtime. You may also set `UNDERTOW_MODULES_DIR` to an absolute directory before starting the console. Without that variable, Undertow looks for `modules/` in the current working directory, beside the Undertow executable, then in its parent directory (the usual `bin/undertow.exe` layout). The artifact stays on the **console host**, not on the agent; no agent redeployment is needed for a new module file.

```text
undertow> modules
undertow> help bof-hello
undertow> help module-wininfo
undertow> help wasm-triage
undertow> use 1
undertow[TALON]> module-wininfo
undertow[TALON]> wasm-triage --background
undertow[TALON]> job output 1
```

## Shipped modules

The repository includes **16 compiled modules** with help sidecars. Start or reattach a console from this checkout so it scans `modules/`, type `modules` to confirm what loaded, then `agents` and `use NUMBER` to select the target. The examples below are commands to type **inside the selected-agent console**. Run `help COMMAND` before a command to see its local help; the module file is sent to the agent when you invoke it.

### WASM host assessment (Windows or Linux agents)

| Console command | What it does | Basic use |
| --- | --- | --- |
| [wasm-triage](../modules/wasm/triage/README.md) | Summarizes host, OS, user, and privilege context. | `wasm-triage` |
| [wasm-inventory](../modules/wasm/inventory/README.md) | Lists processes, services, connections, and neighbours; an optional word filters output. | `wasm-inventory tcp` |
| [wasm-artifact-discovery](../modules/wasm/artifact-discovery/README.md) | Lists interesting file names and metadata under the agent user's home directory or a chosen root; it does not print file contents. | `wasm-artifact-discovery` |
| [wasm-privilege-audit](../modules/wasm/privilege-audit/README.md) | Checks Windows policy and service path configuration; on Linux, checks UID/GID and selected writable paths. | `wasm-privilege-audit` |
| [wasm-persistence-audit](../modules/wasm/persistence-audit/README.md) | Reports service startup and user autoruns. | `wasm-persistence-audit` |
| [wasm-enterprise-posture](../modules/wasm/enterprise-posture/README.md) | Reports domain environment, DNS, and OS policy locations. | `wasm-enterprise-posture` |

For `wasm-artifact-discovery`, add an **agent-side** root such as `wasm-artifact-discovery /home/analyst`; `--stdin ./patterns.txt` adds name patterns from a file on the console host. See the [WASM examples](../modules/wasm/README.md) and each example's README for platform-specific output and build instructions.

### Native modules (Windows AMD64 agents)

| Console command | What it does | Basic use |
| --- | --- | --- |
| [module-hello](../modules/native/README.md) | Demonstrates native arguments, output, status, and cancellation. | `module-hello one "two words"` |
| [module-wininfo](../modules/native/README.md) | Reports Windows computer, process, architecture, and memory information. | `module-wininfo` |
| [module-hostcheck](../modules/native/README.md) | Reports user, token elevation, integrity, privileges, and network adapters. | `module-hostcheck` |
| [module-sift](../modules/native/sift/README.md) | Scans a file or directory with the packaged Sift rules. | `module-sift local C:\Audit --json` |
| [module-askpass](../modules/native/askpass/README.md) | Displays a Windows credential dialog on the agent's desktop. | `module-askpass "Credential test" "Enter test account credentials"` |

`module-askpass` needs a visible interactive Windows desktop; a headless agent session cannot show its dialog. It returns submitted values in module output. `module-sift` can report matched values, so treat its output and saved jobs as sensitive. Replace the example `C:\Audit` with a file or directory on the selected agent.

**Long-running Sift scans:** Sift can run for an extended time across large directories, shares, or a domain; its file and host limits are unset by default. Run `module-sift --background local C:\Audit --json` to keep the console available, then use `jobs`, `job output JOB_ID`, and `job stop JOB_ID` to inspect or stop it. Use `help module-sift` or `module-sift --help` for its local, network, and domain scan modes and optional limits. See the [native examples](../modules/native/README.md), [Sift guide](../modules/native/sift/README.md), and [askpass guide](../modules/native/askpass/README.md).

### BOF compatibility examples (Windows AMD64 agents)

| Console command | What it does | Basic use |
| --- | --- | --- |
| [bof-hello](../modules/bof/README.md) | Demonstrates Beacon output and a Windows import. | `bof-hello` |
| [bof-arguments](../modules/bof/README.md) | Demonstrates typed integer, short, text, wide-text, and binary arguments. | `bof-arguments 123 7 hello world base64:AAEC` |
| [bof-imports](../modules/bof/README.md) | Exercises Windows imports from several DLLs. | `bof-imports` |
| [bof-loaderimports](../modules/bof/README.md) | Exercises runtime loader imports and typed integer parsing. | `bof-loaderimports 0x01020304` |
| [bof-loop](../modules/bof/README.md) | Runs until cancelled; useful for testing background jobs. | `bof-loop --background` |

After starting `bof-loop --background`, run `jobs` to find its job ID and `job stop JOB_ID` to end it. The BOFs are loader and API examples; see the [BOF examples](../modules/bof/README.md) and [compatibility guide](bof-compatibility.md) for inspection, supported imports, and argument formats.

Startup names are `bof-`, `module-`, or `wasm-` followed by the filename stem, lowercased. A final `.x64`, `.amd64`, `.win64`, or `.windows-amd64` before the extension is removed. Thus `network.x64.o` becomes `bof-network` and `wininfo.module` becomes `module-wininfo`. Names must begin with a lowercase ASCII letter, contain only lowercase ASCII letters, digits, and hyphens, and be at most 64 characters. A duplicate name or invalid artifact is reported as a preload warning and skipped. Built-in console commands cannot be replaced.

Use `help` to see every loaded command. `help NAME` shows its usage and original file path; `bofs` lists BOFs and `modules` lists all three formats. The loaded bytes remain usable if the source file changes or disappears during the session. Nothing is persisted in the server. After updating a file, restart the console or use `unload TYPE NAME` followed by `load TYPE FILE [NAME]`.

## Help and argument metadata

Put a JSON sidecar next to the artifact using either `tool.o.json` or `tool.json` (and similarly for `.module` or `.wasm`). The extension-specific name takes precedence. Every packaged artifact has a sidecar with a short description, usage, and help text.

For a native module or WASM file, a sidecar may contain `description`, `usage`, and `help`:

```json
{
  "description": "Read Windows host information",
  "usage": "wininfo",
  "help": "Uses ordinary Windows APIs. No arguments required."
}
```

Native and WASM command arguments are passed as UTF-8 strings through their existing execution paths. Native commands accept `--data FILE`; WASM commands accept `--stdin FILE`. Both accept `--background` before or after command arguments. `--` ends Undertow option parsing for that invocation.

For BOFs, use the existing [BOF manifest](bof-compatibility.md) to describe typed arguments. It supports `name`, `description`, `usage`, `help`, `entrypoint`, and an `arguments` array with `name`, `type`, and `required`. Types are `int`, `short`, `string`, `wstring`, and `binary`. A BOF sidecar is the reliable way to make typed arguments work without an operator typing `--format` each run. Without a sidecar or an explicit format, supplied BOF arguments are encoded as ANSI strings. To assign a format just for the current session, run `load bof FILE ALIAS --format zi` and then invoke `ALIAS` normally.

Use `load bof FILE [NAME]`, `load module FILE [NAME]`, or `load wasm FILE [NAME]` to register a file during the current console session. Manually loaded commands use the given alias, or the unprefixed filename stem when no alias is given. `unload bof|module|wasm NAME` removes a command. BOFs still support `run-bof`, native modules `run-native`, and WASM modules `run-wasm` for one-off use.

The three formats remain separate: `.o` is a Windows AMD64 BOF with the conventional Beacon ABI, `.module` is an Undertow native DLL container with `undertow_native_v1`, and `.wasm` is portable WASI with `undertow_host_v1`. See the [BOF](bof-compatibility.md), [native](native-modules.md), and [WASM](wasm-development.md) guides for compiler requirements and runtime limits.

Back to [documentation home](README.md).
