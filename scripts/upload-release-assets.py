#!/usr/bin/env python3
"""Upload Desktop archives first for legacy updaters which match only OS/arch."""
from pathlib import Path
import re
import subprocess
import sys

ARCHIVE = re.compile(r"^praimate-(server-)?((?:linux|windows|darwin)-[A-Za-z0-9_]+\.(?:tar\.gz|zip))$")


def upload(tag, directory):
    if not re.fullmatch(r"(?:server-)?v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?", tag):
        raise ValueError("Invalid release tag")
    files = sorted(file for file in directory.iterdir() if file.is_file())
    if not files:
        raise ValueError("No staged release assets")
    desktop, server = {}, {}
    for file in files:
        match = ARCHIVE.fullmatch(file.name)
        if match:
            (server if match[1] else desktop)[match[2]] = file
    if not tag.startswith("server-") and server.keys() - desktop.keys():
        raise ValueError("A combined release must provide a Desktop archive for each Server platform")
    first = list(desktop.values())
    remaining = [file for file in files if file not in first]
    # Separate, awaited invocations also repair ordering when all existing
    # assets are replaced: older Desktop clients see their own archive first.
    for group in (first, remaining):
        if group:
            subprocess.run(["gh", "release", "upload", tag, *map(str, group), "--clobber"], check=True)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit("Usage: upload-release-assets.py TAG DIRECTORY")
    upload(sys.argv[1], Path(sys.argv[2]))
