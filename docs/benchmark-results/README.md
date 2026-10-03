# 2026-09-30 measurement data

## 2026-10-01 WebSocket and QUIC live HTTP measurements

The independent [WebSocket](live-websocket-2026-10-01.jsonl) and [QUIC](live-quic-2026-10-01.jsonl) JSONL files contain a metadata line, three repeats of each of five workloads plus the competing workload, and a final server status snapshot. `result.valid` requires every requested response to complete with the exact byte count and SHA-256; the unchanged [`benchmark_http.py`](../../tools/benchmark_http.py) checks both for each flow. The competing record also requires the ten bulk flows to still be running 0.5 seconds after launch. Each carrier has 18 valid workload records and 426 verified responses. The server's agent and client session retransmit counters were both zero after the complete run for each carrier. See [the comparison](../benchmarks.md#live-websocket-and-quic-comparison) for medians and limitations.

The [environment JSON](carrier-environment-2026-10-01.json) records commit, date, OS/CPU, topology, endpoints and TLS mode. The VM client ran `--internal`, accepting the Windows agent's `172.23.224.0/20` route; its default route remained on `eth0`. The server did not create a TUN. The Windows agent served `bench-small.bin` (the bytes 0 through 63, SHA-256 `fdeab9acf3710362bd2658cdc9a29e8f9c757fcf9811603a8c447cd1d9151108`) and a random `one-mib.bin` (1,048,576 bytes, SHA-256 `c23c5f0a2f2b529468ba6f5073a5ed4a5276897371a9d36fffcaab2aca6c81c9`) over `python -m http.server 18080 --bind 172.23.224.1`. The body bytes were not compressed. A fresh random file of the same size can be used with its own SHA-256 supplied to the runner.

To repeat, use matching Undertow builds, enrollment token, pinned server fingerprint, and agent identity key on the three hosts. Start the HTTP server on Windows, then run one carrier at a time. The server listens at `0.0.0.0:443` with `--transport websocket` (TCP/443, path `/undertow`) or `--transport quic` (UDP/443), `--tls-self-signed`, and `--background`. Start the Windows agent with the matching `--transport`, `--server 13.210.247.60:443`, `--tls-insecure-skip-verify`, `--fingerprint f4b9783ee28f46e1fae4067c3caf4dc6e7a15379add7faf369dbf2e8eed5412e`, and `--background`. Start the VM client with the same transport and TLS options plus `--internal --background`; attach to its console and run `agents`, `use 1`, `route accept 172.23.224.0/20`, `background`. Use distinct PID/log/routes files for this run. Verify `ip route get 172.23.224.1` points to `undertow-vpn` before measuring.

Copy [`benchmark_carriers.py`](../../tools/benchmark_carriers.py), [`benchmark_http.py`](../../tools/benchmark_http.py), and the environment JSON to the VM together, then run from the VM for each carrier:

```sh
python3 benchmark_carriers.py \
  --carrier websocket \
  --small-url http://172.23.224.1:18080/bench-small.bin \
  --bulk-url http://172.23.224.1:18080/one-mib.bin \
  --small-sha256 fdeab9acf3710362bd2658cdc9a29e8f9c757fcf9811603a8c447cd1d9151108 \
  --bulk-sha256 c23c5f0a2f2b529468ba6f5073a5ed4a5276897371a9d36fffcaab2aca6c81c9 \
  --commit fd282f39826d0d4d02a3097adc6ed2f6d8a9c043 \
  --metadata-json carrier-environment-2026-10-01.json \
  --output live-websocket.jsonl --repeats 3
```

For QUIC, change `--carrier` and `--output` to `quic` and `live-quic.jsonl`. Stop the VM client, Windows agent, and server between carriers, then start them with the next transport. Finally stop all workers and the HTTP server, and check the VM's default route and temporary TUN removal. The runner invokes the HTTP helper separately for 64-byte requests at 1/10/100 flows, 1 MiB downloads at 1/10 flows, and ten short requests during ten bulk downloads. The results represent this VM → public EC2 → Windows-agent route without artificial loss or shaping.

`shaped-direct-dns-2026-09-30.jsonl` contains one JSON object per RTT/loss/flow cell from `TestShapedDirectDNS`. The full grid has 5 RTTs (5, 25, 50, 100, 200 ms), 6 independent datagram loss settings (0, 1, 2, 5, 10, 20%), and 3 concurrency levels (1, 10, 100), for 90 cells. `small_p50_ms` and `small_p95_ms` describe a burst of 64-byte authenticated session echoes. The fields named `bulk_kib_per_sec` and `bulk_seconds` describe a subsequent burst of **2 KiB echoes**. They are not sustained TCP results. Each cell had a 55-second overall limit and each echo phase a 35-second limit. A zero throughput with an `error` means a phase did not finish by that limit, not zero bytes transmitted. The path shaper is in process and delays or drops complete DNS datagrams in both directions with a fixed deterministic mixer. It does not emulate a resolver, real WAN queueing, or a TUN. Results are one sample per cell and include handshake/setup time outside the reported echo phases.

`shaped-direct-dns-recovery-2026-09-30.jsonl` records the same full grid at `a0a67d0`, where congestion backoff was limited to one event per packet recovery episode but DNS query-error backoff was absent. `shaped-direct-dns-final-2026-09-30.jsonl` records the same full grid at `5bc458d`, which also coalesces DNS query-error backoff by RTT. Both were run on the Windows host and Go version below with the same command, automatic payload profile and shaper. Each contains exactly 90 unique cells and nine phase deadlines. See [the benchmark comparison](../benchmarks.md#final-adaptive-dns-loss-recovery-grid) for the changed deadline cells and the limits of one-run comparisons.

Run on Windows 10.0.26200, Intel Core i9-13950HX, Go 1.25.0, Undertow `c868849`, direct loopback DNS/UDP. The JSONL was generated after `c868849`; adaptive profile was selected automatically. `fragment_size` is the final size; `max_outstanding_queries` and `max_congestion_window` are peaks sampled every 20 ms. `queries_per_sec` and `retransmits` cover the measured echo phases. The transport receive and send packet windows remain capped at 64. Command:

```powershell
$env:UNDERTOW_PERF='1'
$env:UNDERTOW_PERF_RTT='5,25,50,100,200'
$env:UNDERTOW_PERF_LOSS='0,1,2,5,10,20'
$env:UNDERTOW_PERF_FLOWS='1,10,100'
go test -buildvcs=false -timeout 40m ./internal/transport/dns -run '^TestShapedDirectDNS$' -count=1 -v
```

`live-http-2026-09-30.jsonl` records application measurements between a Linux VM client (8 vCPUs, Linux 6.11.2) and a Linux server (2 vCPUs, Linux 6.8.11) on the same public UDP path. The HTTP server served fixed 64-byte and 1 MiB files bound to each tunnel's server-side address. Every completed flow checked byte count and SHA-256. The 64-byte file SHA-256 was `f1c866be5ae1e1049f3bfa685688cde1584403d74b9037f2fc8fce355c9d83cd`; the 1 MiB file SHA-256 was `6f83478748f43951bea13b3fd0e66a78934944471d6274f7579ae561f72105dd`. Undertow used direct DNS/UDP, authenticated encrypted VPN transport, and automatic payload discovery; iodine used 0.7.0, DNS NULL queries, Base128 upstream, Raw downstream, an 1186-byte downstream fragment, lazy mode, EDNS0, and `-r` to skip raw UDP mode. The iodine and Undertow results are each **one run**, sequential rather than simultaneous. They are useful for local acceptance, not a general speed claim. Only the tunnel server address changed in the client URL. DNS query rate and transport window stats were not captured in this live HTTP run; use the shaped data for those Undertow fields. Iodine does not expose a directly comparable congestion window or encrypted-session retransmit count.

Reproduce the HTTP measurements with [the helper](../../tools/benchmark_http.py) after establishing each tunnel and serving the same files:

```sh
python3 tools/benchmark_http.py http://TUNNEL_SERVER_IP:18084/small.bin --flows 100 --bytes 64 --sha256 f1c866be5ae1e1049f3bfa685688cde1584403d74b9037f2fc8fce355c9d83cd
python3 tools/benchmark_http.py http://TUNNEL_SERVER_IP:18084/meg.bin --flows 10 --bytes 1048576 --sha256 6f83478748f43951bea13b3fd0e66a78934944471d6274f7579ae561f72105dd
```

For the competing case, start the ten-flow 1 MiB command in the background, wait 0.5 seconds, run the ten-flow 64-byte command, then wait for the bulk command. No packet shaper was installed on the live path, and the same files and endpoints were used for both transports.

`live-udp-2026-09-30.jsonl` records one run per size/flow cell from the same Linux VM through Undertow to a Windows agent's directly attached internal network. The Windows agent host ran `python tools/benchmark_udp.py serve --bind AGENT_INTERNAL_IP --port 18087`; the VPN client ran the following for each of 64 and 1400 bytes and 1, 10 and 100 flows:

```sh
python3 tools/benchmark_udp.py measure --host AGENT_INTERNAL_IP --port 18087 --flows 100 --bytes 1400 --timeout 10
```

Each flow sends one UDP datagram and validates a reversed echo. The reported throughput is application payload for that short burst, not sustained UDP capacity. All six cells completed without errors. The Linux client had accepted the Windows agent's route through its console, with no server route command.

`live-transfer-2026-09-30.jsonl` records one timed 2 MiB upload and download from the Linux VM client to a Linux agent on that VM, relayed through the public Undertow server. The source was random data; the console reported the byte count and SHA-256 and the helper verified the downloaded file independently. Upload and download used separate destination paths that did not exist. The timer includes console command and completion reporting. Reproduce from the Linux VPN client checkout after starting a detached client and selecting a connected agent (Python `pexpect` 4.9.0 was installed for this run):

```sh
dd if=/dev/urandom of=/tmp/undertow-transfer-source.bin bs=1048576 count=2 status=none
python3 tools/benchmark_transfer.py --pid-file /tmp/undertow-client.pid --agent AGENT_ID --source /tmp/undertow-transfer-source.bin --remote /tmp/undertow-transfer-agent.bin --download /tmp/undertow-transfer-downloaded.bin
```

The helper attaches to the client console, times both commands, checks the completion hashes, verifies the downloaded file, and detaches. The remote and download destinations must be absent before running it. The cancellation check used a separate 16 MiB file and pressed Ctrl-] in the client console during upload; the destination and temporary file were absent afterward.

Back to [documentation home](../README.md).
