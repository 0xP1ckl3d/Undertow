# Performance and regression measurements

Undertow does not yet have a controlled, published comparison with iodine. Results from a local loopback test or an unshaped Internet path cannot establish a general throughput advantage. Record the tool version, operating systems, CPU, path RTT, loss rate, payload profile, flow count, file size, and sample count alongside every result. Use identical hosts, network limits, and application payloads for any comparison.

## Existing repeatable checks

Run from a clean checkout with Go 1.25 or newer:

```sh
go test -timeout 10m ./...
go test ./internal/transport/dns -run '^$' -bench '^BenchmarkDirectDNS$' -benchtime=5x -count=3
go test ./internal/mux -run '^$' -bench '^BenchmarkMultiplexers$' -benchtime=1x -count=3
go test ./internal/mux -run '^$' -bench '^BenchmarkLossyMultiplexers$' -benchtime=1x -count=3
```

`BenchmarkDirectDNS` measures authenticated DNS/UDP echo on loopback at 64, 512, and 4096 byte application payloads. `BenchmarkMultiplexers` compares the custom mux with yamux and smux over the same local ordered carrier at 1, 10, and 100 concurrent streams. `BenchmarkLossyMultiplexers` uses a deterministic simulated carrier with packet loss; it is useful for regressions but is not a live DNS or wide area network result. The session tests also exercise out of order delivery, duplicate packets, ACK/SACK, fast retransmit, and retransmission after loss. A 6 MiB file test checks both directions through the client, server relay, and agent muxes. CI runs the complete test suite separately on Linux and Windows; it does not yet run a Linux to Windows live interoperability test. The benchmark commands are run manually so noisy shared runners do not turn timing variance into a release gate.

## Controlled transport comparison

A defensible Undertow versus iodine result still needs a separate, authorized test environment. Use the same source and destination machines, fixed Go and iodine revisions, fixed UDP path, and the same application payload. Run each case more than once and publish the median, spread, errors, bytes transferred, elapsed time, and transport retransmits. Include at least a clean path, a higher RTT path, and a lossy path; state the exact emulator settings and whether DNS is direct or resolved. Test a single flow and 10 and 100 concurrent flows, with both short requests and sustained transfer. Confirm file hashes after every sustained transfer. Report startup and reconnect time separately from steady state throughput.

Do not combine the existing loopback mux numbers with iodine network numbers in one speed claim. No iodine result is asserted here until both tools are measured under the same conditions.

## Results ledger

| Revision | Path and conditions | Workload | Throughput / latency | Reliability | Status |
| --- | --- | --- | --- | --- | --- |
| Pending | Controlled direct DNS path | Undertow and iodine, matched workloads | — | — | No comparable measurement published |

Add a row only with the full environment and raw command output saved alongside the result. Do not publish enrollment keys, IP addresses belonging to a private test environment, user names, or unredacted host inventory.
