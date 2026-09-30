#!/usr/bin/env python3
"""Repeat verified HTTP workloads over an already established carrier tunnel.

This invokes benchmark_http.py unchanged, so byte counts and SHA-256 are checked
for every completed request. JSONL is flushed after every workload, including
failures, to retain partial runs for diagnosis.
"""

import argparse
import datetime as dt
import json
import pathlib
import subprocess
import sys
import time


HELPER = pathlib.Path(__file__).with_name("benchmark_http.py")


def measure(url, flows, size, sha256, timeout):
    command = [sys.executable, str(HELPER), url, "--flows", str(flows),
               "--bytes", str(size), "--sha256", sha256, "--timeout", str(timeout)]
    completed = subprocess.run(command, capture_output=True, text=True, timeout=timeout + 30)
    if completed.returncode:
        raise RuntimeError(f"helper failed: {completed.stderr.strip()}")
    result = json.loads(completed.stdout)
    result["valid"] = result["completed"] == flows and not result["errors"]
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--carrier", choices=("websocket", "quic"), required=True)
    parser.add_argument("--small-url", required=True)
    parser.add_argument("--bulk-url", required=True)
    parser.add_argument("--small-sha256", required=True)
    parser.add_argument("--bulk-sha256", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--metadata-json", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=120)
    args = parser.parse_args()
    if args.repeats < 1:
        parser.error("--repeats must be positive")

    metadata = json.loads(args.metadata_json.read_text())
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("w", encoding="utf-8") as output:
        def record(value):
            output.write(json.dumps(value, sort_keys=True) + "\n")
            output.flush()
            print(json.dumps(value, sort_keys=True), flush=True)

        record({"type": "metadata", "carrier": args.carrier, "commit": args.commit,
                "started_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
                "small": {"url": args.small_url, "bytes": 64, "sha256": args.small_sha256},
                "bulk": {"url": args.bulk_url, "bytes": 1048576, "sha256": args.bulk_sha256},
                "repeats": args.repeats, "metadata": metadata})
        for repeat in range(1, args.repeats + 1):
            for flows in (1, 10, 100):
                result = measure(args.small_url, flows, 64, args.small_sha256, args.timeout)
                record({"type": "measurement", "workload": "small", "repeat": repeat,
                        "recorded_utc": dt.datetime.now(dt.timezone.utc).isoformat(), "result": result})
            for flows in (1, 10):
                result = measure(args.bulk_url, flows, 1048576, args.bulk_sha256, args.timeout)
                record({"type": "measurement", "workload": "bulk", "repeat": repeat,
                        "recorded_utc": dt.datetime.now(dt.timezone.utc).isoformat(), "result": result})

            bulk_command = [sys.executable, str(HELPER), args.bulk_url, "--flows", "10",
                            "--bytes", "1048576", "--sha256", args.bulk_sha256,
                            "--timeout", str(args.timeout)]
            bulk = subprocess.Popen(bulk_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            time.sleep(0.5)
            overlap_confirmed = bulk.poll() is None
            short = measure(args.small_url, 10, 64, args.small_sha256, args.timeout)
            bulk_stdout, bulk_stderr = bulk.communicate(timeout=args.timeout + 30)
            if bulk.returncode:
                raise RuntimeError(f"competing bulk helper failed: {bulk_stderr.strip()}")
            bulk_result = json.loads(bulk_stdout)
            bulk_result["valid"] = bulk_result["completed"] == 10 and not bulk_result["errors"]
            record({"type": "measurement", "workload": "competing", "repeat": repeat,
                    "recorded_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
                    "overlap_confirmed_at_0_5s": overlap_confirmed,
                    "valid": overlap_confirmed and short["valid"] and bulk_result["valid"],
                    "short": short, "bulk": bulk_result})


if __name__ == "__main__":
    main()
