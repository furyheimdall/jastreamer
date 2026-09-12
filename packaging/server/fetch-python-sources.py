#!/usr/bin/env python3
"""Collect exact upstream source archives for the hash-locked runtime packages."""

import hashlib
import json
import pathlib
import shlex
import sys
import urllib.parse
import urllib.request


def fetch_sources(lock_path, destination):
    destination.mkdir(parents=True, exist_ok=True)
    manifest = []
    for line in lock_path.read_text().replace("\\\n", " ").splitlines():
        fields = shlex.split(line, comments=True)
        if not fields:
            continue
        name, version = fields[0].split("==", 1)
        hashes = {field.removeprefix("--hash=sha256:") for field in fields[1:]}
        metadata_url = f"https://pypi.org/pypi/{name}/{version}/json"
        with urllib.request.urlopen(metadata_url, timeout=30) as response:
            assets = json.load(response)["urls"]
        sources = [asset for asset in assets if asset["packagetype"] == "sdist"]
        if len(sources) != 1:
            raise ValueError(f"Expected one source archive for {name}=={version}")
        source = sources[0]
        expected = source["digests"]["sha256"]
        url = urllib.parse.urlsplit(source["url"])
        filename = source["filename"]
        if expected not in hashes or url.scheme != "https" or url.hostname != "files.pythonhosted.org":
            raise ValueError(f"Untrusted source archive for {name}=={version}")
        if pathlib.Path(filename).name != filename:
            raise ValueError("Invalid source archive filename")
        temporary = destination / (filename + ".partial")
        digest = hashlib.sha256()
        with urllib.request.urlopen(source["url"], timeout=60) as response, temporary.open("wb") as target:
            while chunk := response.read(128 * 1024):
                digest.update(chunk)
                target.write(chunk)
        if digest.hexdigest() != expected:
            temporary.unlink()
            raise ValueError(f"Source checksum mismatch for {filename}")
        temporary.replace(destination / filename)
        manifest.append({"name": name, "version": version, "file": filename, "sha256": expected, "url": source["url"]})
    (destination / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")


if __name__ == "__main__":
    fetch_sources(pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]))
