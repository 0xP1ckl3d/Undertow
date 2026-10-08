# Carrier and callback deployment profiles

Undertow accepts one optional JSON deployment profile on `server`, `client`, and manual `agent` with `--deployment-profile PATH`. The server applies its profile to listeners and uses it as the starting point for newly created payload profiles. A payload profile can use another file with `payload profile create NAME ... deployment-profile=PATH` or `payload profile edit NAME deployment-profile=PATH`; the file is read by the console client and its resolved settings are saved into the payload profile. Existing built agents keep their embedded settings. Use the same QUIC ALPN and WebSocket path on both ends of a connection.

```json
{
  "websocket": {
    "path": "/site/session",
    "headers": {"User-Agent": "SiteClient/1", "X-Deployment": "blue"}
  },
  "quic": {"alpn": "site-carrier/2", "idle_timeout": "2m", "keepalive": "15s"},
  "reconnect": {
    "manual_delay": "2s",
    "progressive_delays": ["2s", "5s", "10s", "30s", "1m", "2m", "5m"],
    "healthy_after": "30s",
    "jitter_percent": 20
  }
}
```

Omitted fields use the existing Undertow values. Durations are Go duration strings. `jitter_percent` accepts 0–50 and spreads each callback wait evenly within that percentage of the scheduled delay; the default is 0. The packaged agent uses the progressive sequence and resets it after an inventory-ready session lasting `healthy_after`. Manual agents, diagnostic probes, and VPN clients use `manual_delay`. Cancellation interrupts the wait.

`--websocket-path` overrides `websocket.path` on the CLI. WebSocket `headers` add ordinary request headers; Undertow reserves Host, upgrade, authentication, proxy authorization, cookies, and `Sec-WebSocket-*` headers so the protocol handshake retains its required form. Avoid putting secrets in this file. QUIC ALPN is negotiated in TLS and must match on both peers. Changing it does not change Undertow's framed session protocol. The QUIC idle timeout and keepalive are connection liveness settings.

## Carrier baseline

| Carrier | Network and runtime behavior | Endpoint files |
| --- | --- | --- |
| Direct DNS | UDP to a numeric server IP and synthetic domain. Payload size is probed in both directions; encrypted session packets ride DNS queries and responses. Polls, outstanding queries, and receive window adapt to conditions. The server listens on UDP/53 by default. | None for the carrier. |
| WebSocket | Persistent TLS over TCP, including HTTP CONNECT when an environment proxy is configured. The default request is a WebSocket upgrade at `/undertow`; the server can also serve hosted payload retrievals on the same HTTPS listener. TCP/443 is the default listen port. | None for the carrier. |
| QUIC | TLS 1.3 over UDP with one bidirectional framed stream. Default ALPN is `undertow/1`, idle timeout is two minutes, and keepalive is 15 seconds. UDP/443 is the default listen port. | None for the carrier. |
| TCP relay | A parent agent opens only an operator-requested TCP listener, forwards a child session over its existing mux connection, and closes the listener with that parent session. | None for the carrier. |
| SMB named-pipe relay | Windows-only operator-requested named pipe carrying the same end-to-end Undertow session. The operator chooses the pipe name. | The pipe is a runtime kernel object, not a filesystem payload. |

An embedded agent generates its Ed25519 identity in memory per process and does not write a config, key, PID, or log during normal operation. Windows native DLL modules and the BOF bridge require a temporary DLL file for the system loader; Undertow removes it after unloading the library. .NET Framework execution creates a temporary worker directory, loads the supplied assembly bytes in memory, and removes that directory when execution finishes. A process crash can leave temporary files behind. Module uploads, downloads, and explicit file operations have their own operator-selected destinations. The Wintun VPN path is separate from ordinary agent carrier operation and installs its bundled DLL beside the executable when needed. See the [module runtime guides](README.md#develop-modules) for execution details.

Windows process inventory now reads the Toolhelp process snapshot in-process for `hostops ps` and WASM `processes`; it returns image name, PID, parent PID, and thread count. Other host operations that require their existing system-tool output remain as they are.

Back to [documentation home](README.md).
