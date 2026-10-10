# ShellPower native module

This module adapts the separate ShellPower project to Undertow's `undertow_native_v1` ABI. It runs Windows PowerShell 5.1 commands in the agent process and sends output through Undertow rather than a process console. The original CLR host, patching behavior, string obfuscation, and PowerShell stream formatting are preserved.

Build from an x64 MSVC Developer PowerShell:

```powershell
./modules/native/shellpower/build.ps1
```

Run an inline command:

```text
shellpower "$PSVersionTable"
shellpower 'Get-Process | Select-Object -First 5'
```

In the Modules tab, select `shellpower`, enter an inline command in **Arguments**, and choose **Stream foreground** or **Run background**. For a local `.ps1` file, leave **Arguments** empty and choose the file under **Optional data file**. The client transfers the script bytes in memory; the agent does not create a script file.

In **Agents → Live shell**, choose **ShellPower · in process** to open the same tool as a persistent session. Undertow uploads this packaged module to the agent and calls its `undertow_shell_main` entry point. One Windows PowerShell 5.1 runspace stays alive for the session, so variables, functions, location changes, and imported modules carry across commands. No `powershell.exe` child process is created. Type `exit` or close the Live shell panel to end the runspace.

The one-shot module accepts UTF-8 and BOM-marked UTF-16LE scripts up to Undertow's 64 KiB native data limit. It returns status 1 when PowerShell reports an error. The live entry point uses PowerShell's asynchronous invocation API and calls `Stop()` when the terminal disconnects, while keeping every CLR operation on the runspace's owning thread.

The CLR/PowerShell reflection and patch layers are copied from the ShellPower/PowerChell project credited in the source project's README. Undertow-specific entry, argument decoding, output routing, cleanup, and build files live in this directory.
