#!/usr/bin/env python3
"""Install only the approved project-local Go toolchain with SHA-256 verification."""
import hashlib
import json
import os
import pathlib
import platform
import shutil
import tarfile
import tempfile
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
VERSION = "go1.27.1"

def main():
    arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine())
    system = {"Linux": "linux", "Darwin": "darwin"}.get(platform.system())
    if not arch or not system:
        raise SystemExit("Use an official Go 1.27.1 toolchain for this platform.")
    target = ROOT / ".tools" / VERSION
    if (target / "bin" / "go").is_file():
        print(f"Toolchain already present: {target}")
        return
    releases = json.loads(urllib.request.urlopen("https://go.dev/dl/?mode=json&include=all", timeout=30).read())
    release = next(r for r in releases if r["version"] == VERSION and r["stable"])
    artifact = next(f for f in release["files"] if f["os"] == system and f["arch"] == arch and f["kind"] == "archive")
    tools = ROOT / ".tools"
    tools.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="toolchain-", dir=tools) as scratch:
        archive = pathlib.Path(scratch) / artifact["filename"]
        urllib.request.urlretrieve("https://go.dev/dl/" + artifact["filename"], archive)
        if hashlib.sha256(archive.read_bytes()).hexdigest() != artifact["sha256"]:
            raise SystemExit("Go archive checksum mismatch; installation refused.")
        with tarfile.open(archive) as tar:
            tar.extractall(scratch, filter="data")
        os.replace(pathlib.Path(scratch) / "go", target)
    print(f"Installed verified {VERSION}: {target}")

if __name__ == "__main__":
    main()
