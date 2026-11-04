#!/usr/bin/env python3
"""
NetEase NeoX Engine NPK Unpacker / Decoder Tool
Usage:
    python3 npk_tool.py <path_to_npk> [output_directory]
"""

import sys
import os
import struct
import zlib
import re
import json

def unpack_npk(npk_path, out_dir=None):
    if not os.path.isfile(npk_path):
        print(f"Error: File not found: {npk_path}")
        return

    if out_dir is None:
        base_name = os.path.splitext(os.path.basename(npk_path))[0]
        out_dir = f"{base_name}_decoded"

    os.makedirs(out_dir, exist_ok=True)
    categories = {
        "python_configs": os.path.join(out_dir, "python_configs"),
        "behavior_trees": os.path.join(out_dir, "behavior_trees"),
        "stage_ai": os.path.join(out_dir, "stage_ai"),
        "xml_configs": os.path.join(out_dir, "xml_configs"),
        "tracks": os.path.join(out_dir, "tracks"),
        "misc": os.path.join(out_dir, "misc"),
    }
    for d in categories.values():
        os.makedirs(d, exist_ok=True)

    with open(npk_path, "rb") as f:
        header = f.read(24)
        magic, count, r1, r2, ver, table_off = struct.unpack("<4sIIIII", header)
        if magic != b"NXPK":
            print(f"Error: Invalid magic bytes {magic}, expected b'NXPK'")
            return

        print(f"Unpacking {count} entries from {npk_path} (table offset: {table_off:#x})...")
        f.seek(table_off)
        entries = [struct.unpack("<IIIIIII", f.read(28)) for _ in range(count)]

        manifest = []
        for i, e in enumerate(entries):
            f1, offset, comp_sz, uncomp_sz, h1, h2, flag = e
            f.seek(offset)
            raw = f.read(comp_sz)
            
            if flag == 1:
                try:
                    data = zlib.decompress(raw)
                except Exception:
                    data = raw
            else:
                data = raw

            cat = "misc"
            ext = "bin"
            internal_name = ""

            if data.startswith(b"# -*- coding: utf-8 -*-") or data.startswith(b"# coding: utf-8") or b"name =" in data[:100] or b"data =" in data[:100]:
                cat = "python_configs"
                ext = "py"
                text = data[:500].decode("utf-8", errors="ignore")
                m = re.search(r"name\s*=\s*[\x22\x27]([^\x22\x27]+)[\x22\x27]", text)
                if m:
                    internal_name = m.group(1).strip()
            elif data.startswith(b"<Behavior"):
                cat = "behavior_trees"
                ext = "xml"
                text = data[:500].decode("utf-8", errors="ignore")
                m = re.search(r"(?:Title|Name)\s*=\s*[\x22\x27]([^\x22\x27]+)[\x22\x27]", text)
                if m:
                    internal_name = m.group(1).strip()
            elif b"<stages>" in data[:100]:
                cat = "stage_ai"
                ext = "xml"
            elif data.startswith(b"<") or data.startswith(b"<?xml") or data.startswith(b"\xef\xbb\xbf<"):
                cat = "xml_configs"
                ext = "xml"
            elif data.startswith(b"NTRK"):
                cat = "tracks"
                ext = "ntrk"

            safe_name = ""
            if internal_name:
                safe_name = "_" + re.sub(r"[^\w\-\u4e00-\u9fff]", "_", internal_name)[:30]

            filename = f"{i:04d}_{f1:08x}{safe_name}.{ext}"
            filepath = os.path.join(categories[cat], filename)

            with open(filepath, "wb") as out_f:
                out_f.write(data)

            manifest.append({
                "index": i,
                "hash_id": f"0x{f1:08x}",
                "offset": offset,
                "compressed_size": comp_sz,
                "uncompressed_size": len(data),
                "category": cat,
                "internal_name": internal_name,
                "file": os.path.relpath(filepath, out_dir)
            })

    manifest_path = os.path.join(out_dir, "manifest.json")
    with open(manifest_path, "w", encoding="utf-8") as mf:
        json.dump(manifest, mf, indent=2, ensure_ascii=False)

    print(f"Done! Decoded {len(manifest)} files into '{out_dir}/'.")
    print(f"Manifest written to '{manifest_path}'.")

def repack_npk(decoded_dir, orig_npk_path, output_npk_path=None):
    if output_npk_path is None:
        output_npk_path = orig_npk_path

    manifest_path = os.path.join(decoded_dir, "manifest.json")
    if not os.path.isfile(manifest_path):
        print(f"Error: Manifest not found: {manifest_path}")
        return False
    if not os.path.isfile(orig_npk_path):
        print(f"Error: Original NPK not found: {orig_npk_path}")
        return False

    with open(manifest_path, "r", encoding="utf-8") as f:
        manifest = json.load(f)

    # Read original table for checksums and flags
    with open(orig_npk_path, "rb") as f:
        hdr = f.read(24)
        magic, count, r1, r2, ver, table_off = struct.unpack("<4sIIIII", hdr)
        f.seek(table_off)
        orig_entries = [struct.unpack("<IIIIIII", f.read(28)) for _ in range(count)]

    # Backup original NPK if outputting directly over it
    if os.path.abspath(orig_npk_path) == os.path.abspath(output_npk_path):
        backup_path = orig_npk_path + ".orig_backup"
        if not os.path.exists(backup_path):
            with open(orig_npk_path, "rb") as fsrc, open(backup_path, "wb") as fdst:
                fdst.write(fsrc.read())
            print(f"  [Backup Created] {orig_npk_path} -> {backup_path}")

    temp_out = output_npk_path + ".tmp"
    print(f"Repacking {len(manifest)} files from '{decoded_dir}' into '{output_npk_path}'...")

    with open(temp_out, "wb") as out_f:
        out_f.write(b"\x00" * 24)  # Placeholder for header
        new_entries = []

        for item, orig_e in zip(manifest, orig_entries):
            fpath = os.path.join(decoded_dir, item["file"])
            with open(fpath, "rb") as in_f:
                data = in_f.read()

            h_id, _, _, _, c1, c2, flag = orig_e
            comp_data = zlib.compress(data, level=6) if flag == 1 else data

            cur_offset = out_f.tell()
            out_f.write(comp_data)
            new_entries.append((h_id, cur_offset, len(comp_data), len(data), c1, c2, flag))

        table_offset = out_f.tell()
        for e in new_entries:
            out_f.write(struct.pack("<IIIIIII", *e))

        out_f.seek(0)
        out_f.write(struct.pack("<4sIIIII", b"NXPK", len(new_entries), 0, 0, 1, table_offset))

    os.replace(temp_out, output_npk_path)
    print(f"Done! Successfully repacked '{output_npk_path}' ({os.path.getsize(output_npk_path)} bytes).")
    return True

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage:")
        print("  Unpack: python3 npk_tool.py <path_to_npk> [output_directory]")
        print("  Repack: python3 npk_tool.py --repack <decoded_dir> <orig_npk> [output_npk]")
        sys.exit(1)

    if sys.argv[1] == "--repack":
        if len(sys.argv) < 4:
            print("Usage: python3 npk_tool.py --repack <decoded_dir> <orig_npk> [output_npk]")
            sys.exit(1)
        d_dir = sys.argv[2]
        o_npk = sys.argv[3]
        out_npk = sys.argv[4] if len(sys.argv) > 4 else o_npk
        repack_npk(d_dir, o_npk, out_npk)
    else:
        npk_file = sys.argv[1]
        out = sys.argv[2] if len(sys.argv) > 2 else None
        unpack_npk(npk_file, out)

