#!/usr/bin/env python3
"""Time Undertow console upload/download through a detached Linux client."""

import argparse
import hashlib
import json
import os
import time

import pexpect


def digest(path):
    value = hashlib.sha256()
    with open(path, "rb") as source:
        for chunk in iter(lambda: source.read(1 << 20), b""):
            value.update(chunk)
    return value.hexdigest()


def measure(child, command, direction, size, expected):
    start = time.monotonic()
    child.sendline(command)
    child.expect(rf"{direction} complete: (\d+) bytes, SHA-256 ([0-9a-f]{{64}})", timeout=120)
    elapsed = time.monotonic() - start
    transferred, reported = int(child.match.group(1)), child.match.group(2).decode()
    if transferred != size or reported != expected:
        raise RuntimeError(f"{direction} integrity mismatch: {transferred} bytes, {reported}")
    child.expect(r"undertow\[[^]]+\]>", timeout=10)
    return {
        "direction": direction.lower(),
        "bytes": transferred,
        "seconds": round(elapsed, 3),
        "kib_per_second": round(transferred / 1024 / elapsed, 2),
        "sha256": reported,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/undertow")
    parser.add_argument("--pid-file", required=True)
    parser.add_argument("--agent", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--remote", required=True)
    parser.add_argument("--download", required=True)
    args = parser.parse_args()
    if os.path.exists(args.remote) or os.path.exists(args.download):
        parser.error("remote and download destinations must not exist")
    size = os.path.getsize(args.source)
    expected = digest(args.source)
    child = pexpect.spawn(
        "sudo", [args.binary, "client", "attach", "--pid-file", args.pid_file],
        encoding=None, timeout=30, dimensions=(40, 200),
    )
    try:
        child.expect(r"undertow>", timeout=30)
        child.sendline("use " + args.agent)
        child.expect(r"undertow\[[^]]+\]>", timeout=30)
        print(json.dumps(measure(child, f"upload {args.source} {args.remote}", "Upload", size, expected)))
        print(json.dumps(measure(child, f"download {args.remote} {args.download}", "Download", size, expected)))
        if digest(args.download) != expected:
            raise RuntimeError("downloaded file hash mismatch")
        child.sendline("background")
        child.expect(pexpect.EOF, timeout=10)
    finally:
        if child.isalive():
            child.close(force=True)


if __name__ == "__main__":
    main()
