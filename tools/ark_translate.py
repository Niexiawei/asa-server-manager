#!/usr/bin/env python3
"""
ARK: Survival Ascended - 翻译插入工具

从 ASA-Translation/*.jsonc 提取 Source→中文 映射，插入到 markdown 表格中。

Usage:
  python tools/ark_translate.py docs/asa-creatureids.md
  python tools/ark_translate.py docs/asa-itemsids.md
  python tools/ark_translate.py docs/asa-engrams.md
  python tools/ark_translate.py docs/asa-creatureids.md --dry-run
  python tools/ark_translate.py docs/asa-creatureids.md --source-col 名称 --target-col 名称（中文）
  python tools/ark_translate.py docs/asa-creatureids.md --show-missed
"""

import json
import re
import sys
import argparse
from pathlib import Path

# ── Paths ─────────────────────────────────────────────────────────────────────
SCRIPT_DIR = Path(__file__).parent
REPO_ROOT   = SCRIPT_DIR.parent
TRANS_DIR   = REPO_ROOT / "ASA-Translation"

TRANS_FILES = [
    "007-飞升附加.jsonc",
    "000-OBT.jsonc",
    "001-Official.jsonc",
]

# custom.json 优先级最高，格式：{"English Name": "中文名", ...}
CUSTOM_FILE = TRANS_DIR / "custom.json"


# ── JSONC parser ──────────────────────────────────────────────────────────────
def parse_jsonc(path: Path) -> object:
    """
    Parse JSONC file:
    - Strip // line comments and /* */ block comments outside strings
    - Remove trailing commas before } or ]
    """
    text = path.read_text(encoding="utf-8")
    buf: list[str] = []
    i = 0
    n = len(text)
    in_str = False

    while i < n:
        c = text[i]
        if in_str:
            if c == "\\" and i + 1 < n:       # escaped char
                buf.append(c)
                buf.append(text[i + 1])
                i += 2
            elif c == '"':                      # end of string
                in_str = False
                buf.append(c)
                i += 1
            else:
                buf.append(c)
                i += 1
        else:
            if c == '"':                        # start of string
                in_str = True
                buf.append(c)
                i += 1
            elif c == "/" and i + 1 < n and text[i + 1] == "/":   # line comment
                while i < n and text[i] != "\n":
                    i += 1
            elif c == "/" and i + 1 < n and text[i + 1] == "*":   # block comment
                i += 2
                while i < n - 1 and not (text[i] == "*" and text[i + 1] == "/"):
                    i += 1
                i += 2
            else:
                buf.append(c)
                i += 1

    stripped = "".join(buf)
    # Remove trailing commas: ,  } or ,  ]
    stripped = re.sub(r",(\s*[}\]])", r"\1", stripped)
    return json.loads(stripped)


# ── Translation extractor ─────────────────────────────────────────────────────
def _extract(node: object, out: dict) -> None:
    """
    Recursively walk the parsed JSONC tree.
    When we find an object with both Source and Trans.SC fields, record it.
    """
    if isinstance(node, dict):
        src = node.get("Source", "")
        trans = node.get("Trans")
        if src and isinstance(trans, dict):
            sc = trans.get("SC", "").strip()
            if sc:
                out[src.strip()] = sc
            return   # don't recurse into a leaf entry
        for v in node.values():
            _extract(v, out)
    elif isinstance(node, list):
        for item in node:
            _extract(item, out)


def build_translation_map(verbose: bool = True) -> tuple[dict, dict]:
    """
    Load all JSONC files and return:
      exact_map  – Source (original case) → SC
      lower_map  – Source.lower()         → SC  (case-insensitive fallback)
    """
    combined: dict[str, str] = {}

    for fname in TRANS_FILES:
        fpath = TRANS_DIR / fname
        if not fpath.exists():
            if verbose:
                print(f"  ⚠  {fname} 未找到，跳过", file=sys.stderr)
            continue
        try:
            data = parse_jsonc(fpath)
            file_map: dict[str, str] = {}
            _extract(data, file_map)
            combined.update(file_map)          # 后加载的文件优先级更高
            if verbose:
                print(
                    f"  ✓  {fname}: {len(file_map):>6} 条，累计 {len(combined):>6} 条",
                    file=sys.stderr,
                )
        except Exception as exc:
            print(f"  ✗  {fname} 解析失败: {exc}", file=sys.stderr)

    # custom.json — 最高优先级，格式：{"English Name": "中文名", ...}
    if CUSTOM_FILE.exists():
        raw = CUSTOM_FILE.read_text(encoding="utf-8").strip()
        if not raw or raw in ("{}", ""):
            if verbose:
                print(f"  -  custom.json 为空，跳过", file=sys.stderr)
        else:
            try:
                custom = json.loads(raw)
                if isinstance(custom, dict):
                    entries = {k.strip(): v.strip() for k, v in custom.items() if k and v}
                    combined.update(entries)
                    if verbose:
                        print(
                            f"  ✓  custom.json:  {len(entries):>6} 条，累计 {len(combined):>6} 条",
                            file=sys.stderr,
                        )
            except Exception as exc:
                print(f"  ✗  custom.json 解析失败: {exc}", file=sys.stderr)
    else:
        if verbose:
            print(f"  -  custom.json 不存在，跳过（路径：{CUSTOM_FILE}）", file=sys.stderr)

    lower_map = {k.lower(): v for k, v in combined.items()}
    return combined, lower_map


# ── Markdown processor ────────────────────────────────────────────────────────
def _extract_name(cell: str) -> str:
    """
    Extract the display name from a markdown table cell.
    Handles: [Name](url)  →  Name
             plain text   →  plain text
             ![alt](url)  →  "" (image cell, skip)
    """
    cell = cell.strip()
    if cell.startswith("!["):          # image cell
        return ""
    m = re.match(r"\[([^\]]+)\]\([^\)]*\)", cell)
    return m.group(1).strip() if m else cell


def _is_separator_row(cells: list[str]) -> bool:
    return all(re.match(r"^-+$", c.strip()) for c in cells if c.strip())


def process_markdown(
    md_path: Path,
    exact_map: dict,
    lower_map: dict,
    source_col: str = "名称",
    target_col: str = "名称（中文）",
    dry_run: bool = False,
    overwrite: bool = False,
) -> dict:
    """
    Insert or update the target_col column in every markdown table that
    contains source_col.

    overwrite=False  → skip cells that already have content (default)
    overwrite=True   → always overwrite with the latest translation
    """
    raw = md_path.read_text(encoding="utf-8")
    lines = raw.splitlines(keepends=True)
    out_lines: list[str] = []

    in_table       = False
    source_idx     = -1
    target_idx     = -1
    has_target_col = False
    stats = {"hits": 0, "misses": [], "unchanged": 0}

    def rebuild(cells: list[str]) -> str:
        return "| " + " | ".join(cells) + " |\n"

    for line in lines:
        row = line.rstrip("\r\n")

        # ── Non-table line ────────────────────────────────────────────────────
        if not row.strip().startswith("|"):
            in_table       = False
            source_idx     = -1
            target_idx     = -1
            has_target_col = False
            out_lines.append(line)
            continue

        # Parse cells (drop leading/trailing empty strings from split)
        parts = row.split("|")
        cells = [c.strip() for c in parts[1:-1]]

        # ── Header row ────────────────────────────────────────────────────────
        if not in_table:
            if source_col not in cells:
                out_lines.append(line)
                continue

            in_table       = True
            source_idx     = cells.index(source_col)
            has_target_col = target_col in cells

            if has_target_col:
                target_idx = cells.index(target_col)
                out_lines.append(line)
            else:
                # Insert target column right after source column
                target_idx = source_idx + 1
                cells = cells[:target_idx] + [target_col] + cells[target_idx:]
                out_lines.append(rebuild(cells))
            continue

        # ── Separator row ─────────────────────────────────────────────────────
        if _is_separator_row(cells):
            if not has_target_col:
                cells = cells[:target_idx] + ["------"] + cells[target_idx:]
            out_lines.append(rebuild(cells))
            continue

        # ── Data row ─────────────────────────────────────────────────────────
        if source_idx < len(cells):
            en_name = _extract_name(cells[source_idx])

            # Check existing value in target column
            if has_target_col and target_idx < len(cells):
                existing = cells[target_idx].strip()
            else:
                existing = ""

            # Skip if already has content and overwrite is off
            if existing and not overwrite:
                stats["unchanged"] += 1
                if not has_target_col:
                    cells = cells[:target_idx] + [existing] + cells[target_idx:]
                out_lines.append(rebuild(cells))
                continue

            # Look up translation
            zh = ""
            if en_name:
                zh = exact_map.get(en_name) or lower_map.get(en_name.lower()) or ""

            if zh:
                stats["hits"] += 1
            elif en_name:
                stats["misses"].append(en_name)

            if has_target_col:
                # Pad if needed
                while len(cells) <= target_idx:
                    cells.append("")
                cells[target_idx] = zh
            else:
                cells = cells[:target_idx] + [zh] + cells[target_idx:]

        out_lines.append(rebuild(cells))

    new_content = "".join(out_lines)
    if not dry_run:
        md_path.write_text(new_content, encoding="utf-8")

    return stats


# ── CLI ───────────────────────────────────────────────────────────────────────
def main() -> None:
    parser = argparse.ArgumentParser(
        description="ARK:SA 翻译插入工具 — 将官方翻译文件中的中文名插入 markdown 表格"
    )
    parser.add_argument("md_file", help="目标 markdown 文件路径")
    parser.add_argument(
        "--source-col", default="名称",
        help="英文名所在列的标题（默认：名称）",
    )
    parser.add_argument(
        "--target-col", default="名称（中文）",
        help="中文名所在列的标题（默认：名称（中文））",
    )
    parser.add_argument(
        "--overwrite", action="store_true",
        help="覆盖已有的中文名（默认只填空格）",
    )
    parser.add_argument(
        "--dry-run", action="store_true",
        help="仅输出统计，不修改文件",
    )
    parser.add_argument(
        "--show-missed", action="store_true",
        help="输出所有未匹配的英文名",
    )
    args = parser.parse_args()

    md_path = Path(args.md_file)
    if not md_path.exists():
        print(f"错误：文件不存在 — {md_path}", file=sys.stderr)
        sys.exit(1)

    print(f"加载翻译文件（来源：{TRANS_DIR}）...", file=sys.stderr)
    exact_map, lower_map = build_translation_map(verbose=True)
    print(f"翻译条目总数：{len(exact_map)}\n", file=sys.stderr)

    print(f"处理：{md_path}", file=sys.stderr)
    stats = process_markdown(
        md_path, exact_map, lower_map,
        source_col=args.source_col,
        target_col=args.target_col,
        dry_run=args.dry_run,
        overwrite=args.overwrite,
    )

    total = stats["hits"] + len(stats["misses"]) + stats["unchanged"]
    prefix = "[DRY RUN] " if args.dry_run else ""
    print(f"\n{prefix}结果：")
    print(f"  命中翻译：  {stats['hits']:>4}")
    print(f"  未找到翻译：{len(stats['misses']):>4}")
    print(f"  已有内容（跳过）：{stats['unchanged']:>4}")
    print(f"  合计行数：  {total:>4}")

    if args.show_missed and stats["misses"]:
        missed_unique = sorted(set(stats["misses"]))
        print(f"\n未匹配的英文名（{len(missed_unique)} 个）：")
        for name in missed_unique:
            print(f"  {name}")

    if not args.dry_run:
        print(f"\n已保存：{md_path}")


if __name__ == "__main__":
    main()
