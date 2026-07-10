#!/usr/bin/env python3
"""
05_filter_cron_enhanced.py
Enhanced cron/heartbeat filter covering patterns missed by original script:
  HEART_BEAT (with underscore), NO_REPLY, NO_ERPLY
Run only if 01_stats_cron_check.py shows >1% new-pattern impact.
Input : 260422_clean_data/00_input_rmcron_mask.jsonl
Output: 260422_clean_data/01_rmcron2.jsonl
"""

import json
import os
import re
import shutil
import sys
import time
from multiprocessing import Pool
from pathlib import Path

INPUT_FILE  = Path("/cpfs01/user/gengzijie.gzj/260422_clean_data/00_input_rmcron_mask.jsonl")
OUTPUT_FILE = Path("/cpfs01/user/gengzijie.gzj/260422_clean_data/01_rmcron2.jsonl")
TMP_DIR     = Path("/cpfs01/user/gengzijie.gzj/260422_clean_data/_tmp_05")

NUM_WORKERS          = 32
CRON_RATIO_THRESHOLD = 0.25

ALL_PATTERNS = re.compile(
    r'\[cron:'
    r'|heartbeat'
    r'|HEART_BEAT'
    r'|NO_REPLY'
    r'|NO_ERPLY'
    r'|A scheduled reminder has been triggered'
    r'|^System: \[.*?\] Exec '
    r'|^System: \[.*?\] 执行 '
    , re.IGNORECASE | re.MULTILINE
)


def _extract_text(content) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for block in content:
            if isinstance(block, dict):
                t = block.get("type")
                if t == "text":
                    parts.append(block.get("text", ""))
                elif t == "tool_result":
                    parts.append(_extract_text(block.get("content", "")))
            elif isinstance(block, str):
                parts.append(block)
        return "\n".join(parts)
    return ""


def find_boundaries(filepath: str, n: int) -> list:
    size = os.path.getsize(filepath)
    chunk = size // n
    bounds = [0]
    with open(filepath, "rb") as f:
        for i in range(1, n):
            f.seek(i * chunk)
            f.readline()
            bounds.append(f.tell())
    bounds.append(size)
    return bounds


def process_chunk(args: tuple) -> dict:
    filepath, start, end, out_path, worker_id = args
    stats = dict(total=0, kept=0, filtered=0, errors=0)
    t0 = time.time()

    with (
        open(filepath, "rb") as fin,
        open(out_path, "w", encoding="utf-8", buffering=8 * 1024 * 1024) as fout,
    ):
        fin.seek(start)
        while fin.tell() < end:
            raw = fin.readline()
            if not raw:
                break
            line = raw.decode("utf-8", errors="replace").rstrip("\n")
            if not line:
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError:
                stats["errors"] += 1
                continue

            stats["total"] += 1
            messages = record.get("messages", [])
            user_turns = [m for m in messages if m.get("role") == "user"]
            total_u = len(user_turns)

            if total_u == 0:
                stats["kept"] += 1
                fout.write(line + "\n")
                continue

            cron_n = sum(
                1 for m in user_turns
                if ALL_PATTERNS.search(_extract_text(m.get("content", "")))
            )

            if cron_n / total_u <= CRON_RATIO_THRESHOLD:
                stats["kept"] += 1
                fout.write(line + "\n")
            else:
                stats["filtered"] += 1

            if stats["total"] % 10000 == 0:
                elapsed = time.time() - t0
                print(
                    f"  [W{worker_id:02d}] {stats['total']:,} records  "
                    f"kept={stats['kept']:,}  filtered={stats['filtered']:,}  "
                    f"rate={stats['total']/elapsed:.0f} rec/s",
                    flush=True,
                )

    elapsed = time.time() - t0
    print(
        f"  [W{worker_id:02d}] DONE  total={stats['total']:,}  "
        f"kept={stats['kept']:,}  filtered={stats['filtered']:,}  elapsed={elapsed:.1f}s",
        flush=True,
    )
    return stats


def main():
    if not INPUT_FILE.exists():
        print(f"ERROR: not found: {INPUT_FILE}", file=sys.stderr)
        sys.exit(1)

    TMP_DIR.mkdir(exist_ok=True)
    size_gb = INPUT_FILE.stat().st_size / 1e9
    print(f"Input : {INPUT_FILE}  ({size_gb:.1f} GB)")
    print(f"Output: {OUTPUT_FILE}")
    print(f"Workers: {NUM_WORKERS}  Threshold: {CRON_RATIO_THRESHOLD*100:.0f}%\n")

    t0 = time.time()
    print("Computing chunk boundaries...")
    bounds = find_boundaries(str(INPUT_FILE), NUM_WORKERS)
    tmp_files = [str(TMP_DIR / f"chunk_{i:02d}.jsonl") for i in range(NUM_WORKERS)]
    job_args  = [(str(INPUT_FILE), bounds[i], bounds[i+1], tmp_files[i], i) for i in range(NUM_WORKERS)]

    print(f"Launching {NUM_WORKERS} workers...\n")
    with Pool(NUM_WORKERS) as pool:
        all_stats = pool.map(process_chunk, job_args)

    print("\nMerging chunk outputs...")
    with open(OUTPUT_FILE, "wb") as fout:
        for tmp in tmp_files:
            with open(tmp, "rb") as f:
                shutil.copyfileobj(f, fout, length=32 * 1024 * 1024)
            Path(tmp).unlink()
    try:
        TMP_DIR.rmdir()
    except OSError:
        pass

    elapsed = time.time() - t0
    total    = sum(s["total"]    for s in all_stats)
    kept     = sum(s["kept"]     for s in all_stats)
    filtered = sum(s["filtered"] for s in all_stats)
    errors   = sum(s["errors"]   for s in all_stats)

    pct = lambda n: f"{n/total*100:.2f}%" if total else "N/A"
    print(f"\n{'='*62}")
    print(f"Elapsed        : {elapsed:.1f}s")
    print(f"Total records  : {total:,}")
    print(f"Kept           : {kept:,}  ({pct(kept)})")
    print(f"Filtered       : {filtered:,}  ({pct(filtered)})")
    print(f"Errors         : {errors:,}")
    out_gb = OUTPUT_FILE.stat().st_size / 1e9
    print(f"Output size    : {out_gb:.2f} GB")
    print(f"{'='*62}")
    print(f"Output: {OUTPUT_FILE}")


if __name__ == "__main__":
    main()
