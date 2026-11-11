#!/usr/bin/env python3
"""Publish canonical bundles and compatibility aliases for old Windows updaters."""
import hashlib
from pathlib import Path
import re
import shutil
import subprocess
import sys

ARCHIVE = re.compile(r"^praimate-(server-)?((?:linux|windows|darwin)-[A-Za-z0-9_]+\.(?:tar\.gz|zip))$")


def prepare_windows_aliases(directory):
    sources = sorted(directory.glob("praimate-windows-*.zip"))
    if not sources:
        return []
    manifest = directory / "SHA256SUMS"
    lines = manifest.read_text().splitlines(keepends=True)
    checksums = {}
    for line in lines:
        match = re.fullmatch(r"([0-9a-fA-F]{64})  ([^\r\n]+)\r?\n?", line)
        if not match or match[2] in checksums:
            raise ValueError("Invalid release checksum manifest")
        checksums[match[2]] = match[1].lower()
    aliases = []
    for source in sources:
        with source.open('rb') as stream:
            digest = hashlib.file_digest(stream, 'sha256').hexdigest()
        if checksums.get(source.name) != digest:
            raise ValueError("Desktop bundle does not match its release checksum")
        alias = directory / source.name.replace("praimate-windows-", "praimate-desktop-windows-", 1)
        shutil.copyfile(source, alias)
        aliases.append(alias)
    # GitHub returns assets by name. These byte-identical names sort before
    # praimate-server-windows-*, unlike the canonical praimate-windows-* names.
    names = {alias.name for alias in aliases}
    kept = [line for line in lines if line.strip().split('  ', 1)[1] not in names]
    if kept and not kept[-1].endswith('\n'):
        kept[-1] += '\n'
    for alias in aliases:
        canonical = alias.name.replace("praimate-desktop-windows-", "praimate-windows-", 1)
        kept.append(checksums[canonical] + '  ' + alias.name + '\n')
    manifest.write_text(''.join(kept))
    return aliases


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
    aliases = prepare_windows_aliases(directory)
    files = sorted(file for file in directory.iterdir() if file.is_file())
    first = sorted(list(desktop.values()) + aliases)
    remaining = [file for file in files if file not in first]
    # Keep compatibility bundles available before refreshing the remaining
    # assets when maintaining an existing release.
    for group in (first, remaining):
        if group:
            subprocess.run(["gh", "release", "upload", tag, *map(str, group), "--clobber"], check=True)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit("Usage: upload-release-assets.py TAG DIRECTORY")
    upload(sys.argv[1], Path(sys.argv[2]))
