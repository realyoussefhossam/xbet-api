#!/usr/bin/env python3
"""Rebuild internal/xbet/data/markets_en.json.gz from 1xbet's official
market-group template dictionary.

Source: https://v3.traincdn.com/genfiles/cms/betstemplates/
  - bets_model_map_{short,full}_en.json   (group-id ranges -> chunk ids)
  - bets_model_{short,full}_en_{chunk}.json (group templates)

Usage: python3 tools/build-dict.py [--cdn https://v3.traincdn.com]
Output: internal/xbet/data/markets_en.json.gz
"""
import argparse
import gzip
import json
import os
import urllib.request

BASE = "/genfiles/cms/betstemplates"
OUT = os.path.join(os.path.dirname(__file__), "..", "internal", "xbet", "data", "markets_en.json.gz")


def fetch(cdn: str, path: str) -> bytes:
    req = urllib.request.Request(cdn + path, headers={"User-Agent": "xbet-api/1.0"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read()


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--cdn", default="https://v3.traincdn.com")
    args = ap.parse_args()

    merged: dict[int, dict] = {}
    for kind in ("short", "full"):
        maps = json.loads(fetch(args.cdn, f"{BASE}/bets_model_map_{kind}_en.json"))
        for chunk_id in maps:
            data = json.loads(fetch(args.cdn, f"{BASE}/bets_model_{kind}_en_{chunk_id}.json"))
            for groups in data.values():
                for gid_s, tpl in groups.items():
                    gid = int(gid_s)
                    entry = merged.setdefault(gid, {"name": "", "markets": {}})
                    if kind == "short":
                        gn = tpl.get("GN")
                        if isinstance(gn, dict) and gn:
                            entry["short"] = next(iter(gn.values()))
                        else:
                            entry["short"] = tpl.get("N", "")
                    else:
                        entry["name"] = tpl.get("N", "")
                    for tid_s, m in (tpl.get("M") or {}).items():
                        tid = int(tid_s)
                        cur = entry["markets"].get(tid)
                        if kind == "short" or cur is None:
                            entry["markets"][tid] = {"n": m.get("N", ""), "t": m.get("T", 0)}
    for g in merged.values():
        g.pop("short", None)

    raw = json.dumps(merged, ensure_ascii=False, separators=(",", ":")).encode()
    with gzip.open(OUT, "wb", 9) as f:
        f.write(raw)
    print(f"groups: {len(merged)}  markets: {sum(len(g['markets']) for g in merged.values())}")
    print(f"wrote {OUT} ({os.path.getsize(OUT)} bytes gzip)")


if __name__ == "__main__":
    main()
