#!/usr/bin/env python3
"""Check that every documentation cross-reference in the repo resolves.

Walks the markdown corpus and every link that points at another .md file,
resolving it relative to the referencing file. Prints unresolved links and
exits non-zero if any are found.

Usage: python3 scripts/check_docs_links.py
"""

import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# A markdown link target, or a `path` inline-code span. We only care about
# references that look like they point at a doc file.
LINK = re.compile(r"\]\(([^)]+)\)|`([A-Za-z0-9_./-]+\.md(?:#[^`]*)?)`")

SCAN_DIRS = ["docs", "infra/runbooks"]
SCAN_FILES = ["README.md"]


def collect():
    out = []
    for d in SCAN_DIRS:
        base = os.path.join(ROOT, d)
        for dirpath, _dirs, files in os.walk(base):
            if "prototype" in dirpath:
                continue
            for f in files:
                if f.endswith(".md"):
                    out.append(os.path.join(dirpath, f))
    for f in SCAN_FILES:
        out.append(os.path.join(ROOT, f))
    return out


def main():
    bad = []
    checked = 0
    for path in sorted(collect()):
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                for m in LINK.finditer(line):
                    target = m.group(1) or m.group(2)
                    if not target or target.startswith(("http", "mailto:")):
                        continue
                    # Strip an anchor.
                    target = target.split("#", 1)[0]
                    if not target or not target.endswith(".md"):
                        continue
                    # "adr/NNNN-slug.md" is the documented MADR naming pattern
                    # in DEVELOPMENT_RULE.md, not a real file.
                    if "NNNN-" in target:
                        continue
                    resolved = os.path.normpath(
                        os.path.join(os.path.dirname(path), target)
                    )
                    checked += 1
                    if not os.path.isfile(resolved):
                        bad.append((os.path.relpath(path, ROOT), target))
    print(f"checked {checked} doc cross-references")
    if bad:
        print(f"UNRESOLVED: {len(bad)}")
        for f, t in bad:
            print(f"  {f}: {t}")
        return 1
    print("all doc cross-references resolve")
    return 0


if __name__ == "__main__":
    sys.exit(main())
