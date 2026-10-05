# Operator accounts and client authentication

Undertow authenticates an operator when a client connects. The server binds that account to the client session. The terminal console and local browser GUI use the same session and show the same identity. The GUI launch URL protects access to that local browser service; it is not another operator account.

## First Team Leader

Create the first account locally before starting a new server. In an interactive terminal, `undertow operators bootstrap` prompts for an account ID and a hidden password; the display name defaults to the ID. You can instead provide `--id` or `UNDERTOW_OPERATOR_ID`, and a private password file or `UNDERTOW_OPERATOR_PASSWORD`. Passwords must be 12–72 bytes. A password file's trailing newline is ignored. Keep any password file readable only by the intended OS account and remove it from temporary locations after provisioning.

```sh
umask 077
printf '%s\n' 'REPLACE_WITH_A_UNIQUE_LONG_PASSWORD' > leader.password
./bin/undertow operators bootstrap --operations-db operations.db --id leader --display-name 'Team Leader' --password-file leader.password
./bin/undertow server
```

The bootstrap command only succeeds while the account database is empty. A server with no operator accounts refuses to start. The database is the server's `--operations-db` (default `operations.db`), so pass the same path to bootstrap and server. The server keeps password hashes and account state there across restarts. Do not copy this database to clients. Back it up as server state.

## Connect a client

Give each person a separate operator account. The password can come from a private file, the `UNDERTOW_OPERATOR_PASSWORD` environment variable, or a hidden prompt on an interactive terminal. The account ID can come from `--operator`, `UNDERTOW_OPERATOR_ID`, or an interactive prompt. This applies to `--vpn`, `--internal`, and `--operator-only` clients:

```sh
undertow client --operator-only --transport quic --server SERVER_IP:443 \
  --fingerprint FINGERPRINT --token-file token.key --tls-insecure-skip-verify \
  --operator alice --operator-password-file alice.password
```

For a terminal start, omit the account flags to be prompted for both values. The password is not echoed. For a service or `--background` start, supply the ID and password through flags/file or environment because there is no prompt. For example, set `UNDERTOW_OPERATOR_ID=alice` and `UNDERTOW_OPERATOR_PASSWORD` in the service environment, then start the client without operator flags. If the console starts a background worker after prompting, it passes the password through that worker's environment and the worker clears the variable after reading it. Do not put the operator password directly in command arguments. A password file and `UNDERTOW_OPERATOR_PASSWORD` cannot be used together.

Connection enrollment (`--auth token|password|none`, `--token-file`, and `--password-file`) still controls the existing transport handshake. Operator credentials are a separate check after that handshake. The server rejects disabled accounts and wrong passwords before registering a client session. Agents do not use operator accounts. `--auth none` does not bypass operator authentication for clients.

## Account management

An authenticated **Operator** can use existing Undertow operations. A **Team Leader** can also manage accounts. In a connected client console:

```text
operators me
operators list
operators create alice "Alice Example" operator alice.password
operators role alice team_leader
operators role alice operator
operators disable alice
operators enable alice
operators reset alice new-alice.password
operators revoke alice
```

`create` and `reset` read the password from a local file in the console; the GUI offers a password input instead. Do not put a password in a command argument. The GUI shows the authenticated account under **Settings → Identity**. Team Leaders can create accounts, change roles, enable or disable accounts, reset passwords, and revoke accounts there. Disabling is reversible. Revocation is permanent and retains the account ID and audit history; IDs cannot be reused. The final active Team Leader cannot be disabled, revoked, or demoted.

Any role, state, or password change closes that account's connected sessions. An enabled account reconnects with its current password; a disabled account cannot reconnect. The server records client actions under the server-bound operator ID and display name, even if a client sends different identity text. Audit history labels this attribution `server_authenticated_operator`.
