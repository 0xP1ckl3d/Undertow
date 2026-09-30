#!/usr/bin/env python3
"""Small UDP echo and concurrency measurement for an Undertow pivot route."""

import argparse
import concurrent.futures
import json
import socket
import statistics
import time


def percentile(values, percent):
    ordered = sorted(values)
    return ordered[(len(ordered) - 1) * percent // 100]


def serve(args):
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
        sock.bind((args.bind, args.port))
        while True:
            payload, peer = sock.recvfrom(65535)
            sock.sendto(payload[::-1], peer)


def measure(args):
    if args.flows < 1 or not 8 <= args.bytes <= 1400:
        raise SystemExit("--flows must be positive; --bytes must be 8..1400")

    def exchange(index):
        payload = index.to_bytes(8, "big") + bytes([index % 251 + 1]) * (args.bytes - 8)
        started = time.monotonic()
        try:
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                sock.settimeout(args.timeout)
                sock.sendto(payload, (args.host, args.port))
                reply, _ = sock.recvfrom(65535)
            elapsed = time.monotonic() - started
            if reply != payload[::-1]:
                return elapsed, "echo mismatch"
            return elapsed, None
        except Exception as exc:
            return time.monotonic() - started, str(exc)

    started = time.monotonic()
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.flows) as pool:
        results = list(pool.map(exchange, range(args.flows)))
    elapsed = time.monotonic() - started
    latencies = [latency for latency, error in results if error is None]
    errors = [error for _, error in results if error is not None]
    print(json.dumps({
        "flows": args.flows,
        "bytes_per_flow": args.bytes,
        "completed": len(latencies),
        "errors": errors,
        "elapsed_seconds": round(elapsed, 3),
        "throughput_kib_per_second": round(len(latencies) * args.bytes / 1024 / elapsed, 2),
        "latency_p50_ms": round(statistics.median(latencies) * 1000, 2) if latencies else None,
        "latency_p95_ms": round(percentile(latencies, 95) * 1000, 2) if latencies else None,
    }, sort_keys=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subcommands = parser.add_subparsers(dest="operation", required=True)
    server = subcommands.add_parser("serve")
    server.add_argument("--bind", required=True)
    server.add_argument("--port", type=int, required=True)
    client = subcommands.add_parser("measure")
    client.add_argument("--host", required=True)
    client.add_argument("--port", type=int, required=True)
    client.add_argument("--flows", type=int, required=True)
    client.add_argument("--bytes", type=int, default=64)
    client.add_argument("--timeout", type=float, default=10)
    args = parser.parse_args()
    if args.operation == "serve":
        serve(args)
    else:
        measure(args)


if __name__ == "__main__":
    main()
