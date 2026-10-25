#!/usr/bin/env python3
"""Remove Claude-generated attribution lines; preserve human authors and prose."""
import pathlib
import re
import sys


def clean(message):
    kept = []
    for line in message.splitlines(keepends=True):
        if re.match(r'^\s*Co-authored-by:\s*Claude(?:\s|<|$)', line, re.I):
            continue
        if re.match(r'^\s*(?:🤖\s*)?Generated (?:with|by)\s+(?:\[)?Claude(?:\s|\]|$)', line, re.I):
            continue
        kept.append(line)
    return ''.join(kept)


if __name__ == '__main__':
    path = pathlib.Path(sys.argv[1])
    original = path.read_bytes()
    text = original.decode('utf-8')
    cleaned = clean(text)
    if cleaned != text:
        path.write_bytes(cleaned.encode('utf-8'))
