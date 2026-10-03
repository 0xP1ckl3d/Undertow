# askpass

See the [module bank](../../../docs/module-bank.md) for every shipped module and its console command.

Undertow Windows AMD64 native module that displays the Windows credential UI, prefills the username with the current security context (`DOMAIN\\user` when available), unpacks the submitted credentials, validates them with `LogonUserW`, and returns the domain, username and password through Undertow module output.

It mirrors the behaviour of Hagrid29/BOF-CredUI with the requested current-user prefill.

## Build

From the Undertow repository:

```powershell
.\modules\native\askpass\build.ps1
```

The build script can bootstrap the x64 MSVC environment itself when Visual Studio Build Tools are installed, then packages the DLL with Undertow's `tools/nativepack`.

Output:

```text
modules\native\askpass\askpass.module
```

## Usage

From a selected Undertow agent:

```text
run-native modules/native/askpass/askpass.module "Windows Security" "Please enter your credentials to continue."
```

Example result:

```text
Valid Credential
        Domain: EXAMPLE
        Username: alice
        Password: example-password
```

If `LogonUserW` rejects the supplied values, the result is labelled `Invalid Credential` but the submitted values are still returned, matching the original BOF's behaviour.

The native module runs in the Undertow agent process and therefore needs an interactive Windows desktop/session for the credential dialog to be visible.


## v1.0.1

Normalises `DOMAIN\\user` before `LogonUserW` validation and uses `CREDUIWIN_ENUMERATE_CURRENT_USER` with the generic credential prompt. Invalid validation output also includes the Win32 `LogonUserW` error code.

Back to [documentation home](../../../docs/README.md).
