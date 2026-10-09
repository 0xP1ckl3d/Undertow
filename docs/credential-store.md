# Engagement Credential Store

Undertow treats one server operations database as an engagement. The Credential Store holds reusable Windows username/password or username/NT-hash material for that engagement. It is separate from the agent Token Store: a credential is reusable authentication material; a token context is a live Windows security object that disappears when its agent process exits. No token handle is placed in the Credential Store.

The vault encrypts each secret with AES-256-GCM and a random nonce. The 32-byte vault key is a separate owner-only file beside the operations database, with the `.credentials.key` suffix. **Back up and restore this key with the database.** If entries exist and the key is missing, server startup fails closed. Restrict filesystem access to the server account and protect both files in backups. The API, GUI, console listings, Jobs, Jump records, audit and history expose IDs, account labels and operator attribution, never decrypted material. Secret-bearing requests are accepted only over the authenticated operator connection. The server decrypts a chosen entry transiently for an authorised operation.

Each entry has an owner. Private entries are visible and usable only by that owner and Team Leaders. An owner can mark an entry shared with other authenticated operators; Team Leaders can manage all entries. Operator account changes and credential deletion are rechecked when queued work dispatches, so revoked access or a removed reference prevents execution. Stored entries survive server restarts; in-memory decrypted copies do not. The server does not reveal a stored secret after saving it. Replacing an entry requires a new secret.

In the GUI, open **Credentials** to save, replace or remove an entry. In **Jump → Start jump**, choose **Stored credential** and select the entry. Jump uses the same existing artifact-delivery and Windows management methods as supplied credentials, with the source agent process context explicitly selected. It does not combine a stored credential with another token context. A queued Jump freezes the credential ID and account label in its durable record; the server resolves the secret only at dispatch. Replacing an entry with a different account or removing it before dispatch fails the Job. NT hashes work only with the Jump methods that already support them: WinRM does not, and Scheduled Task requires its LocalSystem target context.

In an agent's **Tokens** tab, **Create from stored credential** accepts a password entry and a Windows logon type. The server seals the decrypted logon material to a single-use agent creation key; the token and its handle remain in that agent process. NT hashes cannot create a Windows logon token. The agent must be available for the creation-key and create exchanges; expiry or loss between them returns an error and can be retried. Ordinary direct password entry remains available.

The connected operator console provides:

```text
credentials list
credentials add "LABEL" USER DOMAIN password|nt_hash SECRET_FILE [shared]
credentials replace ID "LABEL" USER DOMAIN password|nt_hash SECRET_FILE [shared]
credentials remove ID
jump start ID [INSTALL_PATH] --credential CREDENTIAL_ID
tokens create --credential CREDENTIAL_ID interactive|network|batch|new_credentials
```

Use `DOMAIN -` for no domain. Secret files are read locally and never passed as command arguments. Remove those files using your engagement's normal secret-handling process. The same `credentials` commands are available from an agent's GUI Console; the GUI workspace uses password fields and clears their state after submission.

Validation includes vault encryption, access and restart tests; rejection of a missing key; and a sleeping-agent Jump test proving the durable request contains only the ID and account label. Windows UAT should create an entry for an approved alternate-user account, create a token context from its password reference and verify `exec whoami /user`. It should start an authorised Jump with a stored password and, where appropriate, a stored NT hash, checking both target artifact delivery and the existing remote-management method. Check history, Job output, server logs and the operations database for fixture secrets. Delete the entry before a queued dispatch and confirm the work fails without using the agent process identity or stale plaintext.
