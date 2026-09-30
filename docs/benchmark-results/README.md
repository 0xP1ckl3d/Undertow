# 2026-09-30 measurement data

`shaped-direct-dns-2026-09-30.jsonl` contains one JSON object per RTT/loss/flow cell from `TestShapedDirectDNS`. The full grid has 5 RTTs (5, 25, 50, 100, 200 ms), 6 independent datagram loss settings (0, 1, 2, 5, 10, 20%), and 3 concurrency levels (1, 10, 100), for 90 cells. `small_p50_ms` and `small_p95_ms` describe a burst of 64-byte authenticated session echoes. The fields named `bulk_kib_per_sec` and `bulk_seconds` describe a subsequent burst of **2 KiB echoes**. They are not sustained TCP results. Each cell had a 55-second overall limit and each echo phase a 35-second limit. A zero throughput with an `error` means a phase did not finish by that limit, not zero bytes transmitted. The path shaper is in process and delays or drops complete DNS datagrams in both directions with a fixed deterministic mixer. It does not emulate a resolver, real WAN queueing, or a TUN. Results are one sample per cell and include handshake/setup time outside the reported echo phases.

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
