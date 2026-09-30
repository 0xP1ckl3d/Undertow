#!/usr/bin/env python3
"""Measure concurrent HTTP downloads through an already established tunnel.

The server should host a fixed file and the caller supplies its SHA-256. This
tool measures application throughput and completion latency, not DNS packets.
"""

import argparse
import concurrent.futures
import hashlib
import json
import statistics
import time
import urllib.request


def percentile(values, percent):
    ordered = sorted(values)
    return ordered[(len(ordered) - 1) * percent // 100]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("url")
    parser.add_argument("--flows", type=int, required=True)
    parser.add_argument("--bytes", type=int, required=True)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--timeout", type=float, default=120)
    args = parser.parse_args()
    if args.flows < 1 or args.bytes < 1:
        parser.error("--flows and --bytes must be positive")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def fetch(_):
        started = time.monotonic()
        try:
            with opener.open(args.url, timeout=args.timeout) as response:
                body = response.read()
            elapsed = time.monotonic() - started
            if len(body) != args.bytes:
                return elapsed, f"expected {args.bytes} bytes, received {len(body)}"
            if hashlib.sha256(body).hexdigest() != args.sha256:
                return elapsed, "SHA-256 mismatch"
            return elapsed, None
        except Exception as exc:  # Report every failed flow in the result.
            return time.monotonic() - started, str(exc)

    started = time.monotonic()
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.flows) as pool:
        results = list(pool.map(fetch, range(args.flows)))
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


if __name__ == "__main__":
    main()
