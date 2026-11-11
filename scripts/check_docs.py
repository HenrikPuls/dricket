#!/usr/bin/env python3
"""Consistency checks for the project docs. Exit code 1 on any finding.

1. Every relative Markdown link resolves.
2. Every ADR file is listed in docs/architecture.md section 9, and vice versa.
3. No R-<n> id appears in both specs/roadmap.md and specs/changelog.md
   (the changelog's 'Awaiting verification' list is exempt).
4. README.md and site/index.html reference the newest CHANGELOG.md
   version in their current-release spots, and the newest CHANGELOG
   version is never older than the newest v* git tag.
"""
from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
LINK = re.compile(r"\[[^\]]*\]\(([^)#\s]+)(?:#[^)]*)?\)")
RID = re.compile(r"\bR-\d+\b")


def md_files() -> list[Path]:
    skip = {"node_modules", ".git", ".tools", "dist"}
    return [p for p in ROOT.rglob("*.md") if not skip & set(p.parts)]


def check_links() -> list[str]:
    findings: list[str] = []
    for path in md_files():
        text = path.read_text(encoding="utf-8")
        # ignore fenced code blocks (templates contain placeholder links)
        text = re.sub(r"```.*?```", "", text, flags=re.S)
        for target in LINK.findall(text):
            if re.match(r"^[a-z]+:", target) or target.startswith("<"):
                continue
            if "<" in target:
                continue
            if not (path.parent / target).resolve().exists():
                findings.append(f"{path.relative_to(ROOT)}: broken link {target}")
    return findings


def check_adr_index() -> list[str]:
    adr_dir = ROOT / "docs" / "adr"
    arch = ROOT / "docs" / "architecture.md"
    if not adr_dir.is_dir() or not arch.is_file():
        return []
    files = {p.name for p in adr_dir.glob("[0-9][0-9][0-9][0-9]-*.md")}
    files.discard("0000-template.md")
    text = re.sub(r"```.*?```", "", arch.read_text(encoding="utf-8"), flags=re.S)
    listed = set(re.findall(r"adr/([0-9]{4}-[^)\s]+\.md)", text))
    findings = [f"ADR not in index: {n}" for n in sorted(files - listed)]
    findings += [f"index lists missing ADR: {n}" for n in sorted(listed - files)]
    return findings


def check_ids() -> list[str]:
    road = ROOT / "specs" / "roadmap.md"
    log = ROOT / "specs" / "changelog.md"
    if not (road.is_file() and log.is_file()):
        return []
    log_text = log.read_text(encoding="utf-8")
    shipped = log_text.split("## Shipped", 1)[-1]
    both = set(RID.findall(road.read_text(encoding="utf-8"))) & set(RID.findall(shipped))
    return [f"id in roadmap and changelog: {i}" for i in sorted(both)]


def _ver_tuple(v: str) -> tuple[int, ...]:
    return tuple(int(p) for p in v.lstrip("v").split("."))


def check_versions() -> list[str]:
    """The current-release spots must name the newest CHANGELOG version.

    Historical mentions ("released in v0.4.0") are allowed; the spots that
    state the current release level are checked with explicit patterns, so
    rewording one means updating this check in the same change.
    """
    findings: list[str] = []
    changelog = ROOT / "CHANGELOG.md"
    readme = ROOT / "README.md"
    if not (changelog.is_file() and readme.is_file()):
        return findings
    match = re.search(r"^## \[(\d+\.\d+\.\d+)\]", changelog.read_text(encoding="utf-8"), re.M)
    if not match:
        return ["CHANGELOG.md: no '## [X.Y.Z]' version heading found"]
    current = f"v{match.group(1)}"

    spots = [
        (readme, "release badge", rf"release-{re.escape(current)}-blue"),
        (readme, "status note", rf"early release \({re.escape(current)}\)"),
        (readme, "releases table", rf"releases/tag/{re.escape(current)}"),
        (readme, "install command", rf"cmd/dk@{re.escape(current)}"),
        (ROOT / "site" / "index.html", "status table", rf"released through {re.escape(current)}"),
    ]
    for path, name, pattern in spots:
        if not path.is_file():
            continue
        if not re.search(pattern, path.read_text(encoding="utf-8")):
            findings.append(f"{path.relative_to(ROOT)}: {name} does not reference {current}")

    # The changelog must never lag behind a tag that already exists.
    try:
        out = subprocess.run(
            ["git", "tag", "--list", "v[0-9]*"], cwd=ROOT,
            capture_output=True, text=True, check=True,
        ).stdout.split()
        tags = [t for t in out if re.fullmatch(r"v\d+\.\d+\.\d+", t)]
    except (OSError, subprocess.CalledProcessError):
        tags = []
    if tags:
        latest = max(tags, key=_ver_tuple)
        if _ver_tuple(latest) > _ver_tuple(current):
            findings.append(f"CHANGELOG.md: newest version {current} is older than tag {latest}")
    return findings


def main() -> int:
    findings = check_links() + check_adr_index() + check_ids() + check_versions()
    for f in findings:
        print(f"FAIL {f}")
    if not findings:
        print("docs ok")
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main())
