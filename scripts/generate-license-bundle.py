#!/usr/bin/env python3
"""Build/check notices for the compiled Go server and production web packages."""

import argparse
import json
import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
BUNDLE = ROOT / "third_party" / "THIRD_PARTY_LICENSES.md"
LICENSE_NAME = re.compile(r"^(?:LICENSE|LICENCE|COPYING|NOTICE)(?:[-._].*)?$", re.I)
OVERRIDES = {
    "node_modules/react-remove-scroll-bar@2.3.8": (
        ROOT / "third_party/overrides/npm/react-remove-scroll-bar@2.3.8-LICENSE",
        "Official upstream repository, commit 8ca9ba5ea52de03308fe8ced94f7b159a44d28ff; npm tarball omits the LICENSE file.",
    ),
}


def command(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True)


def license_files(directory):
    if not directory.is_dir():
        raise ValueError(f"missing dependency directory: {directory.name}")
    files = sorted((p for p in directory.iterdir() if p.is_file() and LICENSE_NAME.fullmatch(p.name)), key=lambda p: p.name)
    return files


def append_text(out, heading, path, source=""):
    body = path.read_text(encoding="utf-8").strip()
    if not body:
        raise ValueError(f"empty license text for {heading}")
    out.extend([f"### {heading}", ""])
    if source:
        out.extend([source, ""])
    out.extend(["```text", body, "```", ""])


def go_modules():
    raw = command("go", "list", "-deps", "-json", "./cmd/herdr-space")
    decoder, pos, modules = json.JSONDecoder(), 0, {}
    while pos < len(raw):
        while pos < len(raw) and raw[pos].isspace():
            pos += 1
        if pos >= len(raw):
            break
        package, pos = decoder.raw_decode(raw, pos)
        module = package.get("Module") or {}
        if module.get("Version"):
            if module.get("Replace"):
                raise ValueError(f"replaced Go module needs review: {module['Path']}")
            modules[f"{module['Path']}@{module['Version']}"] = pathlib.Path(module["Dir"])
    if not modules:
        raise ValueError("Go dependency graph was empty")
    return modules


def npm_packages():
    packages = json.loads((ROOT / "web/package-lock.json").read_text(encoding="utf-8"))["packages"]
    result = {}
    for path, item in packages.items():
        if not path or item.get("dev"):
            continue
        version, license_id = item.get("version"), item.get("license")
        if not version or not license_id:
            raise ValueError(f"missing version or license in npm lockfile for {path}")
        directory = ROOT / "web" / path
        package = json.loads((directory / "package.json").read_text(encoding="utf-8"))
        if package.get("version") != version or package.get("license") != license_id:
            raise ValueError(f"npm installation differs from lockfile for {path}")
        result[f"{path}@{version}"] = (directory, license_id)
    if not result:
        raise ValueError("production npm dependency graph was empty")
    return result


def generate():
    out = [
        "# Third-party license texts", "",
        "Generated from pinned compiled Go dependencies and non-development npm lockfile packages.",
        "Run `make license-bundle` after dependency updates and `make license-check` before release.",
        "HERDR is a separately installed Apache-2.0 project and is not included here.", "",
        "## Go standard library", "",
    ]
    go_version = command("go", "env", "GOVERSION").strip()
    append_text(out, go_version, pathlib.Path(command("go", "env", "GOROOT").strip()) / "LICENSE")
    modules = go_modules()
    out.extend(["## Compiled Go modules", ""])
    for name, directory in sorted(modules.items()):
        files = license_files(directory)
        if not files:
            raise ValueError(f"missing Go license text for {name}")
        for file in files:
            append_text(out, f"{name} — {file.name}", file)
    packages = npm_packages()
    out.extend(["## Production npm packages", ""])
    for name, (directory, license_id) in sorted(packages.items()):
        files = license_files(directory)
        override = OVERRIDES.get(name)
        if not files and override:
            path, source = override
            if not path.is_file():
                raise ValueError(f"missing reviewed license override for {name}")
            append_text(out, f"{name} — {license_id}", path, source)
        elif not files:
            raise ValueError(f"missing npm license text for {name}; review upstream and add a versioned override")
        else:
            for file in files:
                append_text(out, f"{name} — {license_id} — {file.name}", file)
    return "\n".join(out).rstrip() + "\n", len(modules), len(packages)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--write", action="store_true")
    mode.add_argument("--check", action="store_true")
    args = parser.parse_args()
    try:
        content, go_count, npm_count = generate()
        if args.write:
            BUNDLE.write_text(content, encoding="utf-8")
            print(f"Wrote license bundle: {go_count} Go modules, {npm_count} production npm packages")
        elif not BUNDLE.is_file() or BUNDLE.read_text(encoding="utf-8") != content:
            raise ValueError("license bundle is stale; run make license-bundle")
        else:
            print(f"License bundle current: {go_count} Go modules, {npm_count} production npm packages")
    except (ValueError, OSError, subprocess.CalledProcessError, json.JSONDecodeError) as error:
        print(f"License bundle error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
