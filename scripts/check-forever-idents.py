#!/usr/bin/env python3
"""Fail if addon Lua uses Forever-illegal keyword+CamelCase identifiers.

Forever's lexer treats names like untilAt / endAt as the keyword (until/end)
plus trailing junk, which aborts the whole addon at parse time. TOC still
lists the addon (IconTexture), so it looks "installed" with no UI.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

KEYWORDS = (
    "and", "break", "do", "else", "elseif", "end", "false", "for", "function",
    "goto", "if", "in", "local", "nil", "not", "or", "repeat", "return", "then",
    "true", "until", "while",
)
PAT = re.compile(r"\b(" + "|".join(KEYWORDS) + r")([A-Z][A-Za-z0-9_]*)\b")
ROOT = Path(__file__).resolve().parents[1] / "addon" / "Sagalore"


def strip_noise(text: str) -> str:
    text = re.sub(r"--\[\[[\s\S]*?\]\]", "", text)
    text = re.sub(r"--[^\n]*", "", text)
    text = re.sub(r"\[\[.*?\]\]", '""', text, flags=re.S)
    text = re.sub(r'"(?:\\.|[^"\\])*"', '""', text)
    text = re.sub(r"'(?:\\.|[^'\\])*'", "''", text)
    return text


def main() -> int:
    bad: list[str] = []
    for path in sorted(ROOT.rglob("*.lua")):
        raw = path.read_text(encoding="utf-8", errors="replace")
        stripped = strip_noise(raw)
        for m in PAT.finditer(stripped):
            line = stripped.count("\n", 0, m.start()) + 1
            bad.append(f"{path.relative_to(ROOT.parent.parent)}:{line}: {m.group(0)}")
    if bad:
        print("Forever-illegal Lua identifiers (keyword + CamelCase):", file=sys.stderr)
        print("\n".join(bad), file=sys.stderr)
        return 1
    print("ok: no Forever-illegal keyword+CamelCase identifiers")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
