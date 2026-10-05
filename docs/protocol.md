# Undertow protocol, version 1

This document describes the current direct DNS protocol implemented by `internal/transport/dns`, `internal/security`, `internal/session`, and `internal/mux`. It is a description of the current implementation, not a promise that later versions will preserve these bytes. All multibyte integers below are unsigned and big endian. Offset ranges are half open.

## Layering and DNS carrier

```text
UDP datagram → DNS message → EDNS private option → handshake or encrypted session packet
encrypted session payload → reassembled mux frame → logical stream
```

The client contacts a server IP literal directly over UDP; this transport does not use a recursive resolver. Each request uses a random 12 byte value encoded as a 24 character hex label before the configured domain. The question is type TXT, class IN. The additional section contains one OPT record with EDNS option code 65001. That option carries the Undertow payload as binary bytes. The response repeats the DNS transaction ID and question name and puts its Undertow payload in the same option. The client ignores delayed responses with a mismatched ID, name, or response flag and rejects a matching response with a nonzero RCODE or empty payload. The DNS message limit is 1232 bytes; the Undertow payload limit is 1050 bytes. No application data is stored in a TXT answer.

The server only accepts queries under its configured synthetic domain. Each DNS query receives at most one response. After authentication, an otherwise idle client keeps one encrypted poll outstanding so the server can return queued data promptly. The server may wait up to one second for outgoing work before answering an idle poll. New queued data reserves an additional query slot so it can be sent immediately while that idle poll is still open. When traffic is queued, the client increases outstanding queries up to 64 according to its congestion window, the peer's advertised receive slots, observed RTT, and recent congestion events. A loss recovery episode halves the DNS query budget once. A burst of failed DNS queries halves it at most once per interval of 500 ms or twice the smoothed RTT, whichever is longer; query failures still count toward the disconnect deadline. Idle polls and data queries on paths with no RTT sample or RTT at least 50 ms have a two-second deadline. Data queries on a measured path below 50 ms retire after 500 ms so a lost query does not occupy an outstanding slot for two seconds. A late response to a retired query is ignored by DNS ID and question name when the socket is reused.

## Handshake and enrollment

After the encrypted carrier handshake and its existing token, password, or open enrollment check, an Undertow **client** sends operator account credentials in its encrypted VPN control hello. The server verifies the password hash in its operations database before registering the client session or returning `ready:true`. The resulting account ID, display name, role, and version are bound to that session; later remote API and interactive audit records derive identity from the bound account. Agents do not send operator credentials. See [Operator authentication](operator-authentication.md).

The first byte identifies an unencrypted handshake message:

| Type | Value | Contents |
| --- | ---: | --- |
| Client hello | 1 | 32 byte client nonce, 32 byte ephemeral X25519 public key, 1 byte payload profile, 40 byte cookie |
| Cookie | 2 | 8 byte 30 second time bucket, 32 byte HMAC-SHA-256 |
| Server hello | 3 | 8 byte nonzero session ID, 32 byte server nonce, 32 byte ephemeral X25519 public key, 32 byte Ed25519 identity public key, 64 byte Ed25519 signature |
| Auth | 4 | Sent inside the encrypted session: 32 byte client identity public key, 64 byte transcript signature, 32 byte enrollment HMAC |
| Auth OK / reject | 5 / 6 | One byte, sent inside the encrypted session |
| Payload probe | 7 | Same-size unauthenticated echo used before the handshake to discover a usable packet size |

An initial hello has a zero cookie. The server sends a cookie tied to the source IP, hello nonce, ephemeral key, profile, and time bucket. The client repeats the hello with that cookie. A valid cookie permits the server to allocate a session and return a signed server hello. If a hello exchange times out, the client retries up to 16 times using its existing nonce and ephemeral key; a repeated cookie bearing hello may create a second unauthenticated server session, which the server expires. The signature covers the fixed transcript label, complete cookie bearing client hello, and server hello bytes before the signature. The client verifies both the signature and the SHA-256 fingerprint of the server's Ed25519 public key. Trust on first use saves that fingerprint only after authentication succeeds; its first connection has the usual interception risk.

The peers derive X25519 shared secret, hash the signed transcript with SHA-256, and use HKDF-SHA-256 to obtain separate AES-256-GCM keys and four byte nonce prefixes for each direction. The client then proves its Ed25519 identity and enrollment secret in the encrypted Auth message. The server identifies an agent by the first 16 bytes of SHA-256 of its identity public key. Token, password, and open enrollment all use the same encrypted transport; open enrollment uses a public all zero enrollment secret and does not restrict who may join. Password enrollment derives its secret with PBKDF2-SHA-256 using the server fingerprint as part of the salt.

Legacy payload profile `0` uses 800 byte fragments and profile `1` uses 320 byte fragments. Adaptive profile `2` extends the hello by a two byte fragment size before the cookie. The cookie and signed transcript bind that size, which must be 128–800 bytes. Before an adaptive handshake, the client tests same-size DNS queries and responses at candidate sizes between 128 and 800 bytes, then chooses the largest passing candidate. The corresponding DNS payload is 84 bytes larger than the fragment (50 byte session header, 18 byte fragment header, and 16 byte authentication tag). A probe response never exceeds its request size. An adaptive session reduces the size of future fragments after three retransmissions of the same packet, avoiding a size reduction for an isolated random loss; 64 successful acknowledgements permit a step upward. Explicit `large` and `small` CLI overrides retain the legacy profiles. An older server that does not support profile `2` needs an explicit legacy profile selection.

## Encrypted session packet

Every encrypted packet has a 50 byte header, zero or one encrypted fragment, and a 16 byte GCM tag. The complete header is authenticated as additional data. The 12 byte GCM nonce is the directional four byte prefix followed by the eight byte packet nonce sequence. Retransmissions reuse the original encrypted packet bytes, including its nonce; a new poll or data packet gets a new nonce sequence.

Encrypted packets can be 106 or 108 bytes long, matching a client hello's length and first byte. The server checks the session ID against existing sessions before classifying either length as a new hello.

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 1 | Packet format version, currently `1` |
| 1 | 1 | `1` for data, `0` for an ACK or poll only packet |
| 2 | 8 | Session ID |
| 10 | 2 | Session protocol version, currently `1` |
| 12 | 8 | Directional packet nonce sequence; never zero |
| 20 | 8 | Reliable data sequence; zero for a poll or ACK only packet |
| 28 | 8 | Cumulative ACK base |
| 36 | 8 | Selective ACK bitmap |
| 44 | 4 | Advertised receive slots |
| 48 | 2 | Encrypted plaintext length |

A data packet's plaintext contains an 18 byte fragment header followed by fragment bytes: message ID (`u64`), whole message length (`u32`), fragment offset (`u32`), and fragment length (`u16`). A complete session message is at most 64 KiB. Fragment size is limited by the signed profile and negotiated size; the receiver checks lengths, bounds, overlap, and duplicate consistency. Incomplete messages expire after 30 seconds. Completed messages have a separate bounded 128-message queue for the mux reader; this does not enlarge the 64-packet wire window. An ACK only packet has no fragment and does not consume a reliable data sequence number. Under adaptive profile `2`, a retransmission lowers the fragment size used for future messages in 64 byte steps down to 128. After 64 successful acknowledgements, it can increase one step toward the discovered ceiling. Already queued or retransmitted fragments retain their original bytes and size; a later reconnect repeats path discovery.

The cumulative ACK base means all reliable data sequences up to that number were received. Bit `0` of the bitmap acknowledges `base+1`; bit `63` acknowledges `base+64`. The receive window is 64 sequences. A received packet outside it is rejected. Packet nonces are tracked to reject replayed data; receiving a duplicate retransmission triggers a fresh ACK in case the earlier ACK was lost. Acknowledged packets leave the pending map, and only packets that were never retransmitted contribute an RTT sample.

The sender begins with a congestion window of 16 packets, capped at 64. It increases by one after a window of new acknowledgements and halves once when a loss recovery episode begins, with a floor of two. The episode covers packets sent through the highest sequence at that first retransmission and ends when the cumulative ACK reaches that sequence; retransmitting other packets in the same episode does not halve the window again. New data must also remain within 64 sequences of the peer's cumulative ACK base; selective ACKs cannot advance that boundary while a gap remains. The base retransmission timeout is three times the smoothed RTT, bounded to 150 ms–3 s, or 500 ms before a sample exists. Each retry doubles its timeout up to 5 s. A gap with at least three higher selective ACKs can trigger a fast retransmission after the gap persists for at least 25 ms or twice the smoothed RTT, whichever is longer; repeated identical SACK evidence does not repeatedly trigger the same gap. Control fragments use a separate priority queue so a bulk backlog does not block stream control.

## Mux frames and stream lifecycle

Each reassembled session message contains one mux frame. Its 20 byte header is:

| Offset | Size | Field |
| ---: | ---: | --- |
| 0 | 1 | Mux frame version, currently `1` |
| 1 | 1 | Frame kind |
| 2 | 8 | Stream ID |
| 10 | 8 | Byte offset or window credit, according to kind |
| 18 | 2 | Frame payload length |

Frame payloads are limited to 700 bytes. Kinds are `OPEN=1`, `OPEN_OK=2`, `OPEN_FAIL=3`, `DATA=4`, `FIN=5`, `RESET=6`, `WINDOW=7`, `PING=8`, `PONG=9`, and `CONTROL=10`. Stream ID zero is reserved for control and ping frames. The proxy/server side allocates even IDs; the opposite side allocates odd IDs. An `OPEN` carries a destination of up to 255 bytes, and the recipient replies `OPEN_OK` or `OPEN_FAIL` before data is used. `DATA` uses byte offsets and a 32 KiB per stream receive window. Reading data returns credit with `WINDOW`. `FIN` closes one sending direction at the final byte offset; the other direction can remain open. `RESET` aborts the stream. Control messages on stream zero carry authenticated session metadata such as mode and readiness.

Agent-side TCP forwards use application destinations on those mux streams. The server opens `listener.undertow.invalid:0` to the selected agent and sends a JSON line with a random 32-character hexadecimal ID and a numeric IPv4 bind address. The agent replies with its bound address or an error and keeps this control stream open for the listener lifetime. For each accepted TCP connection, the agent opens `forward.ID.undertow.invalid:0` to the server. The server opens `client-forward.ID.undertow.invalid:0` to the owning VPN client, sends its loopback target as JSON, waits for the client's `ok` line, then relays TCP bytes in both directions. Closing the control stream, agent session, or client session closes the listener. These destinations add no new DNS or mux frame type; older peers will reject or time out the unsupported application stream.

One-shot arbitrary execution uses `exec.undertow.invalid:0`. Built-in host operations use `hostops.undertow.invalid:0` with the same JSON request/result shapes. The agent checks `exec` and `hostops` capabilities before accepting the respective stream and rejects a built-in request on an exec stream or an argv request on a hostops stream. This separation adds no DNS or mux frame type. Older agents do not recognize the hostops destination and require an updated agent for built-in commands from an updated client.

Interactive command sessions use one bidirectional mux stream to `interactive.undertow.invalid:0` and require the separate `interactive` capability. The opener sends one JSON line with optional `argv`, `cols`, and `rows`. An empty argv starts `/bin/sh` on Linux or `cmd.exe` on Windows. After a `R` ready frame, application frames carry a one-byte kind, four-byte big-endian payload length, and up to 32 KiB of payload: `I` input, `O` output, `Z` terminal size (two big-endian uint16 values), `X` signed 32-bit exit code, and `E` error text. Linux agents allocate a PTY; Windows agents allocate a ConPTY. Both apply size updates and remote terminal echo. Closing or resetting this stream ends the process without closing other mux streams. VPN client consoles relay this stream through `interactive-relay.undertow.invalid:0`, preceded by a JSON line naming the agent ID. The server console uses an authenticated loopback HTTP `CONNECT /v1/agents/{id}/interactive` endpoint to bridge the same stream. Older agents reject the new destination.

Background jobs reuse that interactive application stream. The server owns each job's stream and continuously consumes output while the operator uses or detaches the console. Job create/list/show/output/cancel use authenticated control API paths under `/v1/agents/{id}/jobs` and `/v1/jobs`; VPN client requests carry their session ID as the job owner and are filtered accordingly. Job management adds no DNS or mux frame type.

Memory-backed scripts use `script.undertow.invalid:0` and the separate `scripts` capability. The opener sends a JSON line with `language` (`bash` or `powershell`) and `size`, followed by exactly that many source bytes (at most 1 MiB). After the agent sends `R`, the source travels on the same stream into the selected interpreter's stdin; no script file is created. Output uses the interactive frame envelope: `O` is stdout, `D` is stderr, `X` is the signed exit status and `E` reports a task error. Closing the stream cancels the interpreter. VPN client consoles use `interactive-relay.undertow.invalid:0` with a relay JSON line containing `kind:"script"`; the server console uses authenticated `CONNECT /v1/agents/{id}/script`. Background script jobs use `POST /v1/agents/{id}/scripts/jobs` with base64 source in JSON and the same job ownership, retention and cancellation rules. Source is transmitted inside the authenticated, encrypted Undertow session. Older agents reject the script destination.

Memory-backed WASM uses `wasm.undertow.invalid:0` and the independent `wasm` capability. The opener sends a JSON line with `size`, optional `args`, and optional base64 `stdin` (at most 64 KiB), then sends exactly `size` module bytes after `R` ready. The module limit is 4 MiB. The agent instantiates it directly from memory with wazero's pure-Go interpreter and WASI snapshot preview 1. The guest has no preopened WASI filesystem, inherited environment or WASI network sockets; the separate `undertow_host_v1` imports provide explicit agent-side host and outbound network operations. Guest linear memory is limited to 256 pages (16 MiB), combined stdout/stderr to 4 MiB, execution to two minutes, and concurrency to two agent runs. Output uses `O` stdout, `D` stderr, `E` error and `X` exit frames. Stream closure cancels execution. VPN relays identify `kind:"wasm"`; the server console uses authenticated `CONNECT /v1/agents/{id}/wasm`. Background jobs use `POST /v1/agents/{id}/wasm/jobs` with base64 module bytes in JSON and the existing job ownership and cancellation rules. No new DNS or mux frame type is added; older agents reject the destination. The host import ABI and limits are in the [WASM developer guide](wasm-development.md).

Agent inventory first sends the bounded identity/interface/capability control message. It then sends one or more control messages with `route_update:true`; the first has `reset:true` and may include a separate `default_route`. Each route entry has IPv4 `prefix`, optional `gateway`, `interface`, `type`, `source`, and `direct` fields. Frames remain within the 700-byte control payload limit. The server validates and stores at most 64 candidate routes per agent; it does not install them. VPN clients can accept a candidate prefix from their own console, and client-side collision checks protect existing local networks. Older agents simply have no structured route entries.

## Multi-listener server and relay carrier

Each server carrier listener authenticates peers with the same Ed25519 server identity and enrollment secret, then passes the resulting `transport.Peer` to the same mux and control manager. DNS, WebSocket and QUIC therefore share agent/client registries, route selection, jobs, forwards and the operator API. A peer records the carrier it actually used. Runtime listener control uses the authenticated loopback API (`GET /v1/transports`, `POST /v1/transports/{name}`, `DELETE /v1/transports/{name}`); stopping an active listener requires an explicit `force` query parameter. This does not alter the DNS wire format.

An explicitly enabled relay uses `relay-listener.undertow.invalid:0` as a server-to-parent control stream. The request is a JSON line with a numeric IPv4 `bind`; the response reports the bound address or error, and the stream remains open for the listener lifetime. Each child TCP connection makes the parent open `relay-inbound.undertow.invalid:0` toward the original server and bridge bytes in both directions. Those bytes carry length-prefixed (16-bit big-endian) Undertow handshake and session packets. The server performs the normal identity and enrollment handshake with each child and registers it as a separate agent with its parent ID. The parent carries encrypted child packets and cannot see the child's decrypted mux traffic. The child uses the original server fingerprint. Closing the parent mux closes its listener and descendant paths; the manager deactivates descendant routes. A child can itself host a relay, subject to the eight-link depth limit and ancestor-loop checks. The dedicated `relay` capability guards listener creation independently of `listeners` for TCP service forwards.

On a Windows parent, the relay bind may instead be `\\.\pipe\NAME`. The child reaches it locally or through a `\\HOST\pipe\NAME` SMB path using the `relay-smb` carrier. Its framed handshake and encrypted child session are unchanged. The parent marks inbound streams with `relay-pipe-inbound.undertow.invalid:0`, allowing the server to record `relay-smb` separately from TCP `relay`. No SMB listener starts without an explicit relay command; the Windows named-pipe ACL permits authenticated Windows users and Undertow still verifies child enrollment and the pinned original server identity.

A TCP relay listener can also serve an explicitly enabled artifact URL on the same port. A TLS ClientHello selects the download path; framed Undertow child traffic continues through the relay path. The parent terminates its ephemeral pinned HTTPS certificate, checks an opaque token, then opens `relay-payload.undertow.invalid:0` over its existing authenticated session. The server checks the parent ID, token, artifact state, and SHA-256 before streaming a bounded file with size/hash metadata. `HEAD` retrieves metadata without file bytes. Disabling a URL removes its token without closing the relay listener. A named pipe does not expose an HTTPS URL.

## Versioning and compatibility

The DNS carrier has no separate protocol version field. Version 1 is identified by the legacy handshake shapes, the two session version fields, and the mux frame version. Adaptive profile `2` has a distinct signed hello shape. Unknown payload profiles, malformed handshake lengths, or mismatched session/mux versions are rejected. An adaptive client does not silently downgrade to a legacy profile because path discovery cannot establish whether an old server or a broken path caused the probe failure. Use an explicit legacy profile to connect to an older server.

For the current wire behavior, the source and tests are authoritative. In particular, see `internal/security/handshake_test.go`, `internal/session/session_test.go`, `internal/mux/mux_test.go`, and `internal/transport/dns/codec_test.go`.

Back to [documentation home](README.md).
