#!/usr/bin/env python3
"""List internal Go declarations with zero or one apparent production caller.

This is a candidate generator, not a dead-code oracle. It counts lexical
identifier references so each result still requires source and call-site review.
"""

from __future__ import annotations

import argparse
import os
import re
from collections import Counter, defaultdict
from dataclasses import dataclass
from pathlib import Path


FUNC = re.compile(r"^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*\(")
TYPE = re.compile(r"^\s*type\s+([A-Za-z_]\w*)\b")
IDENTIFIER = re.compile(r"\b[A-Za-z_]\w*\b")


@dataclass(frozen=True)
class Declaration:
    path: Path
    line: int
    kind: str
    name: str


def go_files(root: Path) -> list[Path]:
    ignored = {".git", "coverage", "vendor"}
    found = []
    for directory, children, names in os.walk(root):
        children[:] = [
            child
            for child in children
            if child not in ignored and not child.startswith(".")
        ]
        found.extend(Path(directory, name) for name in names if name.endswith(".go"))
    return found


def declarations(root: Path) -> list[Declaration]:
    found = []
    for path in (root / "internal").rglob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        for line_number, line in enumerate(path.read_text().splitlines(), 1):
            match = FUNC.match(line)
            kind = "func"
            if match is None:
                match = TYPE.match(line)
                kind = "type"
            if match is not None:
                found.append(Declaration(path, line_number, kind, match.group(1)))
    return found


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path.cwd())
    parser.add_argument("--max-production-callers", type=int, default=1)
    args = parser.parse_args()

    root = args.root.resolve()
    files = go_files(root)
    contents = {path: path.read_text() for path in files}
    production_counts = Counter()
    test_counts = Counter()
    package_production_counts: dict[Path, Counter[str]] = defaultdict(Counter)
    package_test_counts: dict[Path, Counter[str]] = defaultdict(Counter)
    for path, content in contents.items():
        counts = Counter(IDENTIFIER.findall(content))
        if path.name.endswith("_test.go"):
            test_counts.update(counts)
            package_test_counts[path.parent].update(counts)
        else:
            production_counts.update(counts)
            package_production_counts[path.parent].update(counts)

    print("path\tline\tkind\tname\tproduction_callers\ttest_references")
    rows = []
    for declaration in declarations(root):
        if declaration.name[:1].isupper():
            production_references = production_counts[declaration.name]
            test_references = test_counts[declaration.name]
        else:
            production_references = package_production_counts[declaration.path.parent][declaration.name]
            test_references = package_test_counts[declaration.path.parent][declaration.name]
        production_callers = max(0, production_references - 1)
        if production_callers <= args.max_production_callers:
            rows.append(
                (
                    production_callers,
                    -test_references,
                    declaration.path.relative_to(root).as_posix(),
                    declaration.line,
                    declaration.kind,
                    declaration.name,
                    test_references,
                )
            )

    for callers, _, path, line, kind, name, tests in sorted(rows):
        print(f"{path}\t{line}\t{kind}\t{name}\t{callers}\t{tests}")


if __name__ == "__main__":
    main()
