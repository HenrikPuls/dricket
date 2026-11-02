#!/usr/bin/env python3
"""Consistency checks for the project docs. Exit code 1 on any finding.

1. Every relative Markdown link resolves.
2. Every ADR file is listed in docs/architecture.md section 9, and vice versa.
3. No R-<n> id appears in both specs/roadmap.md and specs/changelog.md
   (the changelog's 'Awaiting verification' list is exempt).
"""
from __future__ import annotations

import re
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


def main() -> int:
    findings = check_links() + check_adr_index() + check_ids()
    for f in findings:
        print(f"FAIL {f}")
    if not findings:
        print("docs ok")
    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main())
