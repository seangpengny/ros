#!/usr/bin/env python3
import json
import pathlib
import sys


def main() -> int:
    resource_path, metrics_path, summary_path = map(pathlib.Path, sys.argv[1:])
    samples = []
    if resource_path.exists():
        for line in resource_path.read_text(encoding="utf-8").splitlines()[1:]:
            parts = line.split("\t")
            if len(parts) != 3:
                continue
            try:
                samples.append({"timestamp": parts[0], "cpu_percent": float(parts[1]), "rss_kb": int(parts[2])})
            except ValueError:
                pass
    metrics = metrics_path.read_text(encoding="utf-8") if metrics_path.exists() else ""
    required = [
        "ros_game_tick_duration_seconds_total",
        "ros_game_tick_duration_seconds_max",
        "ros_game_active_players",
        "ros_game_snapshot_bytes_total",
        "ros_game_udp_receive_bytes_total",
        "ros_game_udp_send_bytes_total",
    ]
    summary = {
        "resource_sample_count": len(samples),
        "cpu_percent_max": max((sample["cpu_percent"] for sample in samples), default=None),
        "rss_kb_max": max((sample["rss_kb"] for sample in samples), default=None),
        "telemetry_metrics_present": {name: (name in metrics) for name in required},
        "telemetry_complete": all(name in metrics for name in required),
    }
    if samples:
        summary["resource_samples"] = samples
    summary_path.write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    return 0 if summary["telemetry_complete"] and samples else 1


if __name__ == "__main__":
    raise SystemExit(main())
