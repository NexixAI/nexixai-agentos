#!/usr/bin/env python3
import re
import sys
from pathlib import Path


repo_root = Path(__file__).resolve().parents[2]
link_pattern = re.compile(r"\[[^\]]+\]\(([^)]+)\)")
ignore_prefixes = ("http://", "https://", "#", "mailto:")


def resolve_link(md_file: Path, link: str) -> Path:
    target = link.split("#", 1)[0]
    if target.startswith("/"):
        return repo_root / target.lstrip("/")
    return (md_file.parent / target).resolve()


def main() -> int:
    missing = []

    for md_file in repo_root.rglob("*.md"):
        text = md_file.read_text(encoding="utf-8", errors="ignore")
        for match in link_pattern.finditer(text):
            raw_link = match.group(1).strip()
            if raw_link.startswith(ignore_prefixes):
                continue
            resolved = resolve_link(md_file, raw_link)
            if not resolved.exists():
                missing.append((md_file.relative_to(repo_root), raw_link, resolved.relative_to(repo_root)))

    if missing:
        for md_path, link, resolved in missing:
            print(f"{md_path} -> {link} -> {resolved}")
        return 1

    print("All markdown links resolved.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
