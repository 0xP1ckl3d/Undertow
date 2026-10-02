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
