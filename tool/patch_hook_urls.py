#!/usr/bin/env python3
"""
patch_hook_urls.py - Binary patcher for rosauthhook.dylib

Replaces hardcoded 'https://api.roslegacy.net' URLs with a custom local server
address (e.g., 'http://192.168.100.218:8081' or 'http://localhost:8081').
Automatically updates both the C-strings in __cstring and the length fields
in the corresponding __cfstring structures.
"""

import sys
import os
import shutil
import struct

# (original_url, full_path, short_path)
TARGET_URLS = [
    ("https://api.roslegacy.net/v2/ros/login_mobile_ios_v2", "/v2/ros/login_mobile_ios_v2", "/v2/ros/login"),
    ("https://api.roslegacy.net/v2/ros/relaunch_mobile_ios_v2", "/v2/ros/relaunch_mobile_ios_v2", "/v2/ros/relaunch"),
    ("https://api.roslegacy.net/v2/account/rosl/exchangeTicket", "/v2/account/rosl/exchangeTicket", "/v2/account/exchange"),
    ("https://api.roslegacy.net/v2/ros/online-count", "/v2/ros/online-count", "/v2/ros/online"),
]

def update_cfstring_length(data, cstring_offset, new_len):
    """Finds any CFString struct pointing to cstring_offset and updates its length field."""
    updated = 0
    # Search within __DATA_CONST,__cfstring range (approx 0x20000 to 0x22000)
    for addr in range(0x20000, min(len(data) - 32, 0x24000), 8):
        isa, flags, ptr, length = struct.unpack("<QQQQ", data[addr:addr+32])
        # CFString ptr lower 32-bits matches cstring offset
        if (ptr & 0xffffffff) == cstring_offset:
            struct.pack_into("<Q", data, addr + 24, new_len)
            updated += 1
    return updated

def patch_dylib(dylib_path, new_base_url, output_path=None):
    if not os.path.isfile(dylib_path):
        print(f"Error: file not found: {dylib_path}")
        return False

    new_base_url = new_base_url.rstrip("/")

    with open(dylib_path, "rb") as f:
        data = bytearray(f.read())

    patches_applied = 0
    for orig_url_str, full_path, short_path in TARGET_URLS:
        orig_bytes = orig_url_str.encode("utf-8")
        idx = data.find(orig_bytes)
        if idx == -1:
            print(f"Warning: could not find original URL: {orig_url_str}")
            continue

        max_len = len(orig_bytes)
        
        # Try full path first; fall back to short path if necessary
        new_url_str = new_base_url + full_path
        if len(new_url_str.encode("utf-8")) > max_len:
            new_url_str = new_base_url + short_path
            
        new_bytes = new_url_str.encode("utf-8")
        if len(new_bytes) > max_len:
            print(f"Error: even short replacement URL '{new_url_str}' ({len(new_bytes)} bytes) exceeds buffer capacity ({max_len} bytes)")
            return False

        # Zero-pad the replacement string up to the original length
        padded_replacement = new_bytes + b"\x00" * (max_len - len(new_bytes))
        data[idx:idx + max_len] = padded_replacement

        # Update CFString length field
        cf_updated = update_cfstring_length(data, idx, len(new_bytes))
        patches_applied += 1
        print(f"Patched [{hex(idx)}]:\n  Old: {orig_url_str}\n  New: {new_url_str} (padded {max_len - len(new_bytes)} nulls, {cf_updated} CFStrings updated)")

    if patches_applied == 0:
        print("No patches applied.")
        return False

    if output_path is None:
        output_path = dylib_path
        backup_path = dylib_path + ".orig_backup"
        if not os.path.exists(backup_path):
            shutil.copy2(dylib_path, backup_path)
            print(f"Created backup: {backup_path}")

    with open(output_path, "wb") as f:
        f.write(data)

    print(f"\nSuccessfully wrote patched dylib ({patches_applied} URLs updated) to: {output_path}")
    return True

def main():
    if len(sys.argv) < 3:
        print("Usage:")
        print("  python3 tool/patch_hook_urls.py <path_to_rosauthhook.dylib> <new_base_url> [output_path]")
        print("\nExample:")
        print("  python3 tool/patch_hook_urls.py ipa/RoS-Legacy-iOS/client.app/rosauthhook.dylib http://192.168.100.218:8081")
        sys.exit(1)

    dylib_path = sys.argv[1]
    new_base_url = sys.argv[2]
    out_path = sys.argv[3] if len(sys.argv) > 3 else None
    success = patch_dylib(dylib_path, new_base_url, out_path)
    sys.exit(0 if success else 1)

if __name__ == "__main__":
    main()
