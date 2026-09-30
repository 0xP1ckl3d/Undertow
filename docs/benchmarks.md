# Performance and reliability measurements

These results were measured on 2026-09-30. The raw records and exact setup are in [benchmark results](benchmark-results/README.md). They are single runs on the stated hosts and path, so treat differences as observations rather than a general speed claim. Every completed HTTP flow checked its byte count and SHA-256.

## Test paths and workloads

The controlled DNS test runs direct authenticated DNS/UDP over an in-process path shaper on Windows 10.0.26200, i9-13950HX, Go 1.25.0, revision `3a1c72f`. It covers all 90 combinations of RTT 5/25/50/100/200 ms, independent datagram loss 0/1/2/5/10/20%, and 1/10/100 concurrent echo streams. The first phase exchanges 64-byte messages; the second exchanges 2 KiB messages. The latter is an echo burst, **not sustained TCP**. Each cell has a 55-second limit and each phase a 35-second limit. The JSONL includes latency, echo throughput, retransmits, duplicates, DNS query rate, peak congestion window, peak outstanding queries, and final payload fragment size.

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

## Controlled DNS path

The complete 90-cell matrix is in [shaped-direct-dns-2026-09-30.jsonl](benchmark-results/shaped-direct-dns-2026-09-30.jsonl). The shaper delays or drops complete DNS datagrams in both directions with a fixed deterministic mixer. It does not emulate a resolver, WAN queueing, or a TUN. A cell with an `error` reached a phase deadline, so its zero throughput fields do not mean zero bytes crossed the path.

All 1-flow and 10-flow cells completed. At **each** RTT, the 100-flow cells completed at 0–5% loss and reached the deadline at 10% and 20% loss: 80 completed cells and 10 deadline cells overall. Selected 100-flow results show how sharply random loss affects this version:

| RTT / loss | 64-byte median / p95 | 2 KiB echo throughput | Queries/s | Retransmits | Peak cwnd / outstanding |
| --- | ---: | ---: | ---: | ---: | ---: |
| 5 ms / 0% | 26.8 / 43.5 ms | 2529.0 KiB/s | 1816.8 | 0 | 31 / 16 |
| 5 ms / 5% | 26.1 / 4085.6 ms | 24.2 KiB/s | 22.9 | 16 | 20 / 16 |
| 25 ms / 0% | 109.8 / 193.7 ms | 429.5 KiB/s | 345.2 | 0 | 32 / 31 |
| 25 ms / 5% | 134.2 / 10904.7 ms | 16.4 KiB/s | 14.2 | 16 | 18 / 18 |
| 100 ms / 0% | 409.1 / 914.9 ms | 126.8 KiB/s | 94.4 | 0 | 32 / 31 |
| 100 ms / 5% | 716.5 / 16264.8 ms | 13.7 KiB/s | 11.0 | 15 | 19 / 19 |
| 200 ms / 0% | 808.8 / 1413.8 ms | 56.6 KiB/s | 45.7 | 0 | 32 / 31 |
| 200 ms / 5% | 1208.4 / 12043.8 ms | 11.9 KiB/s | 9.8 | 25 | 18 / 18 |

This loss sensitivity and long tail latency are significant remaining performance limits. The shaper's independent loss in both DNS directions means one query/response exchange can lose either datagram. A timeout is an incomplete test workload under the stated deadline, rather than evidence of corrupt data. All completed echoes were checked for size and content.

The adaptive profile was selected automatically: payload size is probed in both directions before the session, outstanding DNS queries grow with queued traffic and path health, and idle polling backs off while retaining a quick first-packet slot. The encrypted send and receive packet windows are capped at 64. In the shaped data, `fragment_size` is the final size, and the congestion and outstanding query values are peaks sampled every 20 ms. Query rate and retransmits cover the echo phases. The live iodine run did not expose comparable encrypted retransmit or congestion-window counters, so those metrics are reported for Undertow's shaped path only.

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

Use [benchmark_http.py](../tools/benchmark_http.py) for the matched HTTP requests and [benchmark_udp.py](../tools/benchmark_udp.py) for UDP pivot echo. Run each on the named host shown in the [result metadata](benchmark-results/README.md); the tools print machine-readable JSON. The regular benchmarks remain available for local regressions:

```sh
go test ./internal/transport/dns -run '^$' -bench '^BenchmarkDirectDNS$' -benchtime=5x -count=3
go test ./internal/mux -run '^$' -bench '^BenchmarkMultiplexers$' -benchtime=1x -count=3
go test ./internal/mux -run '^$' -bench '^BenchmarkLossyMultiplexers$' -benchtime=1x -count=3
```

The mux benchmarks use a local ordered carrier or a simulated lossy carrier. Keep their values separate from the live DNS and iodine results.
