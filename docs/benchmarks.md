# Performance and reliability measurements

The DNS and iodine results below were measured on 2026-09-30; the WebSocket/QUIC comparison was measured on 2026-10-01. The raw records and exact setups are in [benchmark results](benchmark-results/README.md). Treat differences as observations on the stated hosts and path, not general speed claims. Every completed HTTP flow checked its byte count and SHA-256.

## Live WebSocket and QUIC comparison

Three sequential repeats per workload used Undertow `fd282f39826d0d4d02a3097adc6ed2f6d8a9c043` on a Linux 6.11.2 VM client (8 logical CPUs, Core i9-13950HX), an EC2 Linux 6.8.11 server (2 vCPUs, Xeon E5-2686 v4), and a Windows 11 10.0.26200 agent (Core i9-13950HX). The client used `--internal` to reach a Python HTTP server on the agent at `172.23.224.1:18080` via the EC2 public endpoint `13.210.247.60:443`. WebSocket used TCP/443 and `/undertow`; QUIC used UDP/443. Both used an ephemeral self-signed TLS certificate, explicit skip-CA-verification opt-in, and a pinned Undertow server fingerprint. There was no packet shaper. The exact host, endpoint, TLS, file and hash metadata are in the [environment record](benchmark-results/carrier-environment-2026-10-01.json).

Each cell is the median of three runs, with the latency pair showing the median of each run's request median and p95 (ms). Throughput is aggregate verified application payload, in KiB/s. The competing test started ten 1 MiB transfers, waited 0.5 seconds, then issued ten 64-byte requests. All 852 HTTP responses completed with the expected byte count and SHA-256; each run's result and the server's final session counters are in the [WebSocket](benchmark-results/live-websocket-2026-10-01.jsonl) and [QUIC](benchmark-results/live-quic-2026-10-01.jsonl) JSONL files.

| Workload | Flows | WebSocket | QUIC |
| --- | ---: | ---: | ---: |
| 64-byte HTTP median / p95 | 1 | 32.2 / 32.2 ms | 31.7 / 31.7 ms |
| 64-byte HTTP median / p95 | 10 | 51.1 / 63.1 ms | 42.1 / 44.9 ms |
| 64-byte HTTP median / p95 | 100 | 175.3 / 1096.1 ms | 92.1 / 1097.0 ms |
| 1 MiB HTTP aggregate | 1 | 1756.9 KiB/s | 1896.0 KiB/s |
| 1 MiB HTTP aggregate | 10 | 2424.8 KiB/s | 2689.1 KiB/s |
| 64-byte HTTP median / p95 during ten bulk flows | 10 | 211.9 / 216.2 ms | 211.8 / 222.1 ms |
| Ten 1 MiB flows with competing short requests | 10 | 2400.5 KiB/s | 2833.3 KiB/s |

QUIC's ten-flow bulk median was 11% higher than WebSocket's on this path. It still runs Undertow's ACK/retransmission/window protocol over one ordered QUIC stream; the server reported **zero Undertow retransmits** for both agent and client sessions in each carrier run, so these measurements do not demonstrate double retransmission. Both reached the 64-packet Undertow congestion window. The 100-flow p95 varied widely across repeats (WebSocket 623–1121 ms; QUIC 585–1104 ms), and one QUIC competing run had a 710 ms short-request p95 while the other two were 203–222 ms. Those tails could include stream head-of-line effects, HTTP server scheduling, or path variation; this clean-path test does not isolate them. No QUIC code change was justified by these measurements. Lossy-path behavior and available-bandwidth saturation were not measured.

## Test paths and workloads

The controlled DNS test runs direct authenticated DNS/UDP over an in-process path shaper on Windows 10.0.26200, i9-13950HX, Go 1.25.0. Full grids were run at baseline revision `c868849`, recovery revision `a0a67d0`, and final revision `5bc458d`. Each covers all 90 combinations of RTT 5/25/50/100/200 ms, independent datagram loss 0/1/2/5/10/20%, and 1/10/100 concurrent echo streams. The first phase exchanges 64-byte messages; the second exchanges 2 KiB messages. The latter is an echo burst, **not sustained TCP**. Each cell has a 55-second limit and each phase a 35-second limit. The JSONL includes latency, echo throughput, retransmits, duplicates, DNS query rate, peak congestion window, peak outstanding queries, and final payload fragment size.

The live comparison used a Linux VM client (Linux 6.11.2, 8 vCPUs) and a Linux server (Linux 6.8.11, 2 vCPUs) over the same public UDP path, without a packet shaper. Both tunnels served identical 64-byte and 1 MiB files from the tunnel server address. Undertow used direct DNS/UDP, encrypted transport, and automatic payload discovery. The baseline was iodine 0.7.0 using DNS NULL queries, Base128 upstream, Raw downstream, 1186-byte downstream fragments, EDNS0 and lazy mode, with raw UDP mode disabled. The tools ran sequentially and each workload has one sample.

## Live Undertow and iodine results

Latency is median/p95 of completed individual requests. Bulk throughput is aggregate application payload throughput. All recorded HTTP requests completed with correct hashes.

| Workload | Flows | Undertow | iodine 0.7.0 |
| --- | ---: | ---: | ---: |
| 64-byte HTTP | 1 | 39.5 / 39.5 ms | 46.8 / 46.8 ms |
| 64-byte HTTP | 10 | 103.3 / 107.1 ms | 130.4 / 172.6 ms |
| 64-byte HTTP | 100 | 459.8 / 1363.5 ms | 1466.2 / 2108.4 ms |
| 1 MiB HTTP | 1 | 495.6 KiB/s | 119.5 KiB/s |
| 1 MiB HTTP | 10 | 495.8 KiB/s | 144.4 KiB/s |
| 64-byte HTTP during ten 1 MiB flows | 10 | 918.6 / 926.9 ms | 1596.8 / 1633.5 ms |
| Ten 1 MiB flows with competing short requests | 10 | 435.0 KiB/s | 143.2 KiB/s |

The competing test started ten bulk flows, waited 0.5 seconds, then issued ten short requests. Neither tunnel provided low interactive latency under that load. The raw records are in [live-http-2026-09-30.jsonl](benchmark-results/live-http-2026-09-30.jsonl).

UDP pivot echo was measured from the Linux VM through Undertow to the Windows agent's internal address. Each flow sent one datagram and checked the reversed reply. All flows completed; this tests datagram forwarding and concurrent request latency, not sustained UDP saturation.

| Datagram | Flows | Median / p95 | Application throughput |
| --- | ---: | ---: | ---: |
| 64 bytes | 1 | 32.8 / 32.8 ms | 1.8 KiB/s |
| 64 bytes | 10 | 125.1 / 126.5 ms | 4.8 KiB/s |
| 64 bytes | 100 | 791.6 / 810.6 ms | 7.4 KiB/s |
| 1400 bytes | 1 | 44.9 / 44.9 ms | 29.4 KiB/s |
| 1400 bytes | 10 | 123.4 / 131.9 ms | 94.0 KiB/s |
| 1400 bytes | 100 | 591.5 / 673.8 ms | 190.6 KiB/s |

Raw UDP data is in [live-udp-2026-09-30.jsonl](benchmark-results/live-udp-2026-09-30.jsonl).

A 2 MiB file was timed through the client console, server relay, and Linux agent on the live Undertow path. Upload took 6.494 seconds (315.4 KiB/s); download took 5.712 seconds (358.6 KiB/s). Both completion hashes and the downloaded file's SHA-256 matched the source. These are one run each, with progress reporting enabled. The [raw transfer records](benchmark-results/live-transfer-2026-09-30.jsonl) include bytes and elapsed time. A separate 16 MiB upload was cancelled around 10%; it left no destination or partial file and the console remained usable.

## Controlled DNS path: baseline

The complete 90-cell matrix is in [shaped-direct-dns-2026-09-30.jsonl](benchmark-results/shaped-direct-dns-2026-09-30.jsonl). The shaper delays or drops complete DNS datagrams in both directions with a fixed deterministic mixer. It does not emulate a resolver, WAN queueing, or a TUN. A cell with an `error` reached a phase deadline, so its zero throughput fields do not mean zero bytes crossed the path.

All 1-flow and 10-flow cells completed. Of the 100-flow cells, 21 completed and nine reached the deadline: 81 completed cells overall. The timeout cells were 20% loss at 5 and 25 ms; 10% and 20% at 50 and 100 ms; and 5%, 10% and 20% at 200 ms. Selected 100-flow results show how sharply random loss affects this version:

| RTT / loss | 64-byte median / p95 | 2 KiB echo throughput | Queries/s | Retransmits | Peak cwnd / outstanding |
| --- | ---: | ---: | ---: | ---: | ---: |
| 5 ms / 0% | 24.4 / 44.5 ms | 2418.1 KiB/s | 1734.2 | 0 | 31 / 16 |
| 5 ms / 5% | 50.2 / 2748.7 ms | 30.3 KiB/s | 30.8 | 17 | 18 / 16 |
| 25 ms / 0% | 127.6 / 272.3 ms | 315.5 KiB/s | 263.5 | 0 | 32 / 31 |
| 25 ms / 5% | 163.0 / 8396.2 ms | 22.1 KiB/s | 18.4 | 17 | 18 / 18 |
| 100 ms / 0% | 427.3 / 745.7 ms | 115.1 KiB/s | 91.0 | 0 | 32 / 31 |
| 100 ms / 5% | 629.8 / 11233.1 ms | 13.4 KiB/s | 10.8 | 26 | 18 / 18 |
| 200 ms / 0% | 824.7 / 1842.9 ms | 63.0 KiB/s | 46.8 | 0 | 32 / 31 |
| 200 ms / 5% | 1030.2 / 11320.7 ms | timed out | 10.3 | 17 | 19 / 19 |

This loss sensitivity and long tail latency are significant remaining performance limits. The shaper's independent loss in both DNS directions means one query/response exchange can lose either datagram. A timeout is an incomplete test workload under the stated deadline, rather than evidence of corrupt data. All completed echoes were checked for size and content. An earlier full grid with the fixed two-second query deadline had ten timeouts; the low-RTT data-query deadline in `c868849` reduced that to nine in this single rerun. One high-RTT 5% cell timed out in the rerun after completing previously, so this is insufficient to claim a stable improvement across the full grid.

The adaptive profile was selected automatically: payload size is probed in both directions before the session, outstanding DNS queries grow with queued traffic and path health, and idle polling backs off while retaining a quick first-packet slot. The encrypted send and receive packet windows are capped at 64. In the shaped data, `fragment_size` is the final size, and the congestion and outstanding query values are peaks sampled every 20 ms. Query rate and retransmits cover the echo phases. The live iodine run did not expose comparable encrypted retransmit or congestion-window counters, so those metrics are reported for Undertow's shaped path only.

## Final adaptive DNS loss-recovery grid

The [final 90-cell records](benchmark-results/shaped-direct-dns-final-2026-09-30.jsonl) are from `5bc458d`. Packet congestion now halves once per recovery episode, and failed DNS queries reduce the query budget at most once per 500 ms or twice the measured RTT, whichever is longer. Automatic payload discovery, the 64-packet send/receive bounds, reordered-response handling, and priority control traffic remain in place. The [intermediate grid](benchmark-results/shaped-direct-dns-recovery-2026-09-30.jsonl) at `a0a67d0` omitted query-error backoff and exposed a low-loss throughput regression; the final revision restores a bounded version of that signal.

The final grid completed 81/90 cells, the same total as the baseline. Its nine deadline cells were 100 flows at 5 and 25 ms/20% loss; 50 ms/10% and 20%; 100 ms/10% and 20%; and 200 ms/10% and 20%, plus 10 flows at 100 ms/20%. The 200 ms/5%/100-flow cell completed in the final run after timing out in the baseline, while the 100 ms/20%/10-flow cell moved the other way. These are single runs, so neither change proves a stable completion-rate improvement.

At 5% loss and 100 flows, the following pairs show baseline → final 64-byte request p95 latency and 2 KiB echo throughput. Echo content was verified in every completed cell.

| RTT | Small-request p95 | 2 KiB echo throughput |
| --- | ---: | ---: |
| 5 ms | 2749 → 1107 ms | 30.3 → 56.7 KiB/s |
| 25 ms | 8396 → 3713 ms | 22.1 → 55.7 KiB/s |
| 50 ms | 13737 → 7382 ms | 15.8 → 16.8 KiB/s |
| 100 ms | 11233 → 8333 ms | 13.4 → 15.6 KiB/s |
| 200 ms | 11321 → 11035 ms | timed out → 12.9 KiB/s |

Other cells varied in both directions. For example, 50 ms/1%/100 flows measured 116.0 KiB/s in the baseline grid and 57.0 KiB/s in the final grid, with worse small-request p95; targeted repeats of this cell varied substantially with query scheduling. The measured result is an improvement for several 5% cells, **not** a blanket speedup or a fix for high-loss tail latency. Nine deadline cells and multi-second tails remain the significant performance limit.

## Live feature and interoperability checks

The Linux VM reached a Windows agent's internal network through Undertow using ICMP, TCP and UDP. An internal-only client accepted the agent's route from its own console without a server route command; its default Internet route stayed intact. VPN-only and combined VPN/internal mode both installed the two `/1` routes and used the server's public egress. A 1.5 MiB file was uploaded and downloaded through the Linux agent with matching SHA-256 hashes and no-overwrite errors on repeat. Three simultaneous TCP listener forwards across two agents remained usable after console detach; stopping one agent removed only its listeners. Built-in host operations, Bash and PowerShell scripts, WASM, jobs, capability reporting, and console reattach were exercised across the Linux and Windows hosts, including foreground and background tasks.

The Windows agent, Linux server, and Linux client interoperated live. A Windows **client** Wintun run could not be accepted on this non-elevated Windows shell because device installation returned `Access is denied`; it still requires an elevated live run. High-loss, high-concurrency shaped DNS cells that hit their deadline remain a performance limitation. Neither condition is counted as a passing case.

## Repeat the measurements

Run the full Go suite and the opt-in controlled grid from a clean checkout:

```powershell
go test -buildvcs=false -timeout 10m ./...
$env:UNDERTOW_PERF='1'
$env:UNDERTOW_PERF_RTT='5,25,50,100,200'
$env:UNDERTOW_PERF_LOSS='0,1,2,5,10,20'
$env:UNDERTOW_PERF_FLOWS='1,10,100'
go test -buildvcs=false -timeout 40m ./internal/transport/dns -run '^TestShapedDirectDNS$' -count=1 -v
```

Use [benchmark_http.py](../tools/benchmark_http.py) for the matched HTTP requests, [benchmark_udp.py](../tools/benchmark_udp.py) for UDP pivot echo, and [benchmark_transfer.py](../tools/benchmark_transfer.py) to time console uploads/downloads on Linux with `pexpect`. Run each on the named host shown in the [result metadata](benchmark-results/README.md); the tools print machine-readable JSON. The regular benchmarks remain available for local regressions:

```sh
go test ./internal/transport/dns -run '^$' -bench '^BenchmarkDirectDNS$' -benchtime=5x -count=3
go test ./internal/mux -run '^$' -bench '^BenchmarkMultiplexers$' -benchtime=1x -count=3
go test ./internal/mux -run '^$' -bench '^BenchmarkLossyMultiplexers$' -benchtime=1x -count=3
```

The mux benchmarks use a local ordered carrier or a simulated lossy carrier. Keep their values separate from the live DNS and iodine results.
