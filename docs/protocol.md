# Undertow protocol, version 1

This document describes the current direct DNS protocol implemented by `internal/transport/dns`, `internal/security`, `internal/session`, and `internal/mux`. It is a description of the current implementation, not a promise that later versions will preserve these bytes. All multibyte integers below are unsigned and big endian. Offset ranges are half open.

## Layering and DNS carrier

```text
UDP datagram → DNS message → EDNS private option → handshake or encrypted session packet
encrypted session payload → reassembled mux frame → logical stream
```

The client contacts a server IP literal directly over UDP; this transport does not use a recursive resolver. Each request uses a random 12 byte value encoded as a 24 character hex label before the configured domain. The question is type TXT, class IN. The additional section contains one OPT record with EDNS option code 65001. That option carries the Undertow payload as binary bytes. The response repeats the DNS transaction ID and question name and puts its Undertow payload in the same option. The client rejects a response with a mismatched ID, name, response flag, RCODE, or empty payload. The DNS message limit is 1232 bytes; the Undertow payload limit is 1050 bytes. No application data is stored in a TXT answer.

The server only accepts queries under its configured synthetic domain. Each DNS query receives at most one response. After authentication, an otherwise idle client still sends encrypted polls so the server can return queued data. The server may wait up to 200 ms for outgoing work before answering an idle poll. The current client runs 16 poll workers, each with a two second query deadline; this count is fixed in version 1.

## Handshake and enrollment

The first byte identifies an unencrypted handshake message:

| Type | Value | Contents |
| --- | ---: | --- |
| Client hello | 1 | 32 byte client nonce, 32 byte ephemeral X25519 public key, 1 byte payload profile, 40 byte cookie |
| Cookie | 2 | 8 byte 30 second time bucket, 32 byte HMAC-SHA-256 |
| Server hello | 3 | 8 byte nonzero session ID, 32 byte server nonce, 32 byte ephemeral X25519 public key, 32 byte Ed25519 identity public key, 64 byte Ed25519 signature |
| Auth | 4 | Sent inside the encrypted session: 32 byte client identity public key, 64 byte transcript signature, 32 byte enrollment HMAC |
| Auth OK / reject | 5 / 6 | One byte, sent inside the encrypted session |

An initial hello has a zero cookie. The server sends a cookie tied to the source IP, hello nonce, ephemeral key, profile, and time bucket. The client repeats the hello with that cookie. A valid cookie permits the server to allocate a session and return a signed server hello. The signature covers the fixed transcript label, complete cookie bearing client hello, and server hello bytes before the signature. The client verifies both the signature and the SHA-256 fingerprint of the server's Ed25519 public key. Trust on first use saves that fingerprint only after authentication succeeds; its first connection has the usual interception risk.

The peers derive X25519 shared secret, hash the signed transcript with SHA-256, and use HKDF-SHA-256 to obtain separate AES-256-GCM keys and four byte nonce prefixes for each direction. The client then proves its Ed25519 identity and enrollment secret in the encrypted Auth message. The server identifies an agent by the first 16 bytes of SHA-256 of its identity public key. Token, password, and open enrollment all use the same encrypted transport; open enrollment uses a public all zero enrollment secret and does not restrict who may join. Password enrollment derives its secret with PBKDF2-SHA-256 using the server fingerprint as part of the salt.

Payload profile `0` uses 800 byte session fragments; profile `1` uses 320 byte fragments. The profile is part of the signed hello. The client can retry with profile `1` after a session failure, but version 1 does not perform path MTU negotiation during a connection.

## Encrypted session packet

Every encrypted packet has a 50 byte header, zero or one encrypted fragment, and a 16 byte GCM tag. The complete header is authenticated as additional data. The 12 byte GCM nonce is the directional four byte prefix followed by the eight byte packet nonce sequence. Retransmissions reuse the original encrypted packet bytes, including its nonce; a new poll or data packet gets a new nonce sequence.

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

A data packet's plaintext contains an 18 byte fragment header followed by fragment bytes: message ID (`u64`), whole message length (`u32`), fragment offset (`u32`), and fragment length (`u16`). A complete session message is at most 64 KiB. Fragments are limited to 800 or 320 bytes by the negotiated profile; the receiver checks lengths, bounds, overlap, and duplicate consistency. Incomplete messages expire after 30 seconds. An ACK only packet has no fragment and does not consume a reliable data sequence number.

The cumulative ACK base means all reliable data sequences up to that number were received. Bit `0` of the bitmap acknowledges `base+1`; bit `63` acknowledges `base+64`. The receive window is 64 sequences. A received packet outside it is rejected. Packet nonces are also tracked to reject replayed packets. Acknowledged packets leave the pending map, and only packets that were never retransmitted contribute an RTT sample.

The sender begins with a congestion window of 16 packets, capped at 64. It increases by one after a window of new acknowledgements and halves on retransmission, with a floor of two. New data must also remain within 64 sequences of the peer's cumulative ACK base; selective ACKs cannot advance that boundary while a gap remains. The base retransmission timeout is three times the smoothed RTT, bounded to 150 ms–3 s, or 500 ms before a sample exists. Each retry doubles its timeout up to 5 s. A gap with at least three higher selective ACKs can trigger a fast retransmission; repeated identical SACK evidence does not repeatedly trigger the same gap. Control fragments use a separate priority queue so a bulk backlog does not block stream control.

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

## Versioning and compatibility

The DNS carrier has no separate protocol version field. Version 1 is identified by the fixed handshake shapes, the two session version fields, and the mux frame version. Unknown payload profiles, malformed handshake lengths, or mismatched session/mux versions are rejected. There is currently no version negotiation or downgrade mechanism. A future wire change needs an explicit compatibility design and tests; changing a field while retaining version `1` would break interoperability or invalidate authentication assumptions.

For the current wire behavior, the source and tests are authoritative. In particular, see `internal/security/handshake_test.go`, `internal/session/session_test.go`, `internal/mux/mux_test.go`, and `internal/transport/dns/codec_test.go`.
