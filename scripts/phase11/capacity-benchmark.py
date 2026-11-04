#!/usr/bin/env python3
import datetime
import json
import os
import pathlib
import re
import shlex
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request


ROOT = pathlib.Path(__file__).resolve().parents[2]


def env_int(name, default):
    try:
        return int(os.environ.get(name, default))
    except ValueError:
        return default


def env_float(name, default):
    try:
        return float(os.environ.get(name, default))
    except ValueError:
        return default


def metric_values(text):
    values = {}
    for line in text.splitlines():
        fields = line.split()
        if len(fields) >= 2:
            try:
                values[fields[0]] = float(fields[1])
            except ValueError:
                pass
    return values


def fetch(url):
    with urllib.request.urlopen(url, timeout=3) as response:
        return response.read().decode("utf-8")


def command_from_env(name, default):
    return shlex.split(os.environ.get(name, default))


def sample_process(pid):
    try:
        output = subprocess.check_output(
            ["ps", "-p", str(pid), "-o", "%cpu=", "-o", "rss="],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
        cpu, rss = output.split()
        return float(cpu), int(rss)
    except (OSError, subprocess.CalledProcessError, ValueError):
        return None, None


def main():
    run_id = os.environ.get(
        "CAPACITY_RUN_ID",
        datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ"),
    )
    run_dir = pathlib.Path(
        os.environ.get("CAPACITY_OUTPUT_DIR", str(ROOT / "artifacts" / "capacity" / run_id))
    )
    run_dir.mkdir(parents=True, exist_ok=True)
    records_path = run_dir / "records.jsonl"
    server_log_path = run_dir / "game-server.log"

    listen_addr = os.environ.get("GAME_LISTEN_ADDR", "127.0.0.1:19000")
    health_addr = os.environ.get("GAME_HEALTH_ADDR", "127.0.0.1:19080")
    profiles = [int(value) for value in os.environ.get("CAPACITY_PROFILES", "2 10 25 50 100").split()]
    repetitions = max(1, env_int("CAPACITY_REPETITIONS", 1))
    wait_ms = max(1, env_int("CAPACITY_WAIT_MS", 6000))
    max_tick_overruns = env_int("CAPACITY_MAX_TICK_OVERRUNS", 0)
    max_rtt_ms = env_float("CAPACITY_MAX_RTT_MS", 250.0)

    game_default = str(ROOT / "bin" / "game-server")
    protocol_default = str(ROOT / "bin" / "protocol-client")
    game_command = command_from_env("GAME_SERVER_CMD", game_default)
    protocol_command = command_from_env("PROTOCOL_CLIENT_CMD", protocol_default)
    if not pathlib.Path(game_command[0]).exists():
        print(f"capacity benchmark requires {game_command[0]}", file=sys.stderr)
        return 2
    if not pathlib.Path(protocol_command[0]).exists():
        print(f"capacity benchmark requires {protocol_command[0]}", file=sys.stderr)
        return 2

    base_env = os.environ.copy()
    base_env.update(
        {
            "ROS_GAME_LISTEN_ADDR": listen_addr,
            "ROS_GAME_HEALTH_ADDR": health_addr,
            "ROS_GAME_MATCH_DURATION": os.environ.get("ROS_GAME_MATCH_DURATION", "60s"),
            "ROS_GAME_STAGING_DURATION": os.environ.get("ROS_GAME_STAGING_DURATION", "0s"),
            "ROS_GAME_ATTACK_COOLDOWN": os.environ.get("ROS_GAME_ATTACK_COOLDOWN", "1ms"),
        }
    )

    server = None
    records = []
    try:
        with server_log_path.open("w", encoding="utf-8") as server_log:
            server = subprocess.Popen(
                game_command,
                cwd=ROOT,
                env=base_env,
                stdout=server_log,
                stderr=subprocess.STDOUT,
                text=True,
            )
            ready_url = f"http://{health_addr}/readyz"
            for _ in range(60):
                try:
                    fetch(ready_url)
                    break
                except (urllib.error.URLError, TimeoutError):
                    if server.poll() is not None:
                        print(f"game server exited early; see {server_log_path}", file=sys.stderr)
                        return 1
                    time.sleep(0.25)
            else:
                print(f"game server did not become ready; see {server_log_path}", file=sys.stderr)
                return 1

            for players in profiles:
                for repetition in range(1, repetitions + 1):
                    log_path = run_dir / f"load-{players}-players-{repetition}.log"
                    protocol_env = base_env.copy()
                    protocol_env.update(
                        {
                            "ROS_TEST_SUITE": "load",
                            "ROS_TEST_PLAYERS": str(players),
                            "ROS_TEST_WAIT_MS": str(wait_ms),
                            "ROS_TEST_LATENCY_MS": "0",
                            "ROS_TEST_PACKET_LOSS_PCT": "0",
                            "ROS_TEST_DISCONNECT_AFTER_MS": "0",
                            "ROS_TEST_RECONNECT": "0",
                            "ROS_GAME_SERVER_ADDR": listen_addr,
                        }
                    )
                    try:
                        completed = subprocess.run(
                            protocol_command,
                            cwd=ROOT,
                            env=protocol_env,
                            capture_output=True,
                            text=True,
                            timeout=max(30, wait_ms / 1000 + 20),
                            check=False,
                        )
                        output = completed.stdout + completed.stderr
                        protocol_rc = completed.returncode
                    except subprocess.TimeoutExpired as error:
                        output = (error.stdout or "") + (error.stderr or "") + "\ntimeout\n"
                        protocol_rc = 124
                    log_path.write_text(output, encoding="utf-8")

                    metrics_text = fetch(f"http://{health_addr}/metrics")
                    metrics_path = run_dir / f"metrics-{players}-players-{repetition}.txt"
                    metrics_path.write_text(metrics_text, encoding="utf-8")
                    values = metric_values(metrics_text)
                    cpu, rss = sample_process(server.pid)
                    rtt_match = re.search(r"rtt_ms=([0-9]+(?:\.[0-9]+)?)", output)
                    rtt_ms = float(rtt_match.group(1)) if rtt_match else None
                    record = {
                        "players": players,
                        "repetition": repetition,
                        "protocol_exit_code": protocol_rc,
                        "tick_overruns": values.get("ros_game_tick_overruns_total"),
                        "tick_duration_seconds_max": values.get("ros_game_tick_duration_seconds_max"),
                        "active_players": values.get("ros_game_active_players"),
                        "snapshot_bytes_total": values.get("ros_game_snapshot_bytes_total"),
                        "udp_receive_bytes_total": values.get("ros_game_udp_receive_bytes_total"),
                        "udp_send_bytes_total": values.get("ros_game_udp_send_bytes_total"),
                        "cpu_percent_sample": cpu,
                        "rss_kb_sample": rss,
                        "rtt_ms": rtt_ms,
                        "thresholds": {
                            "max_tick_overruns": max_tick_overruns,
                            "max_rtt_ms": max_rtt_ms,
                        },
                    }
                    record["passed"] = (
                        protocol_rc == 0
                        and record["tick_overruns"] is not None
                        and record["tick_overruns"] <= max_tick_overruns
                        and rtt_ms is not None
                        and rtt_ms <= max_rtt_ms
                    )
                    records.append(record)
                    with records_path.open("a", encoding="utf-8") as records_file:
                        records_file.write(json.dumps(record, sort_keys=True) + "\n")
    finally:
        if server is not None and server.poll() is None:
            server.send_signal(signal.SIGTERM)
            try:
                server.wait(timeout=5)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait()

    passed = bool(records) and all(record["passed"] for record in records)
    summary = {
        "run_id": run_id,
        "environment": "local dedicated game-server process",
        "listen_addr": listen_addr,
        "wait_ms": wait_ms,
        "profiles": sorted({record["players"] for record in records}),
        "repetitions": repetitions,
        "record_count": len(records),
        "passed": passed,
        "max_observed_rtt_ms": max(
            (record["rtt_ms"] for record in records if record["rtt_ms"] is not None),
            default=None,
        ),
        "max_observed_tick_duration_seconds": max(
            (
                record["tick_duration_seconds_max"]
                for record in records
                if record["tick_duration_seconds_max"] is not None
            ),
            default=None,
        ),
        "max_observed_cpu_percent_sample": max(
            (
                record["cpu_percent_sample"]
                for record in records
                if record["cpu_percent_sample"] is not None
            ),
            default=None,
        ),
        "max_observed_rss_kb_sample": max(
            (record["rss_kb_sample"] for record in records if record["rss_kb_sample"] is not None),
            default=None,
        ),
        "records": records,
    }
    report_json = run_dir / "capacity-report.json"
    report_md = run_dir / "capacity-report.md"
    report_json.write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    lines = [
        "# Capacity benchmark",
        "",
        f"Run: {run_id}",
        "",
        "This is a reproducible local benchmark of the independent authoritative game server. It is not evidence of ROS Legacy wire compatibility or a production capacity guarantee.",
        "",
        f"- Profiles: {summary['profiles']}",
        f"- Records: {summary['record_count']}",
        f"- Result: {'PASS' if passed else 'FAIL'}",
        f"- Maximum observed protocol RTT: {summary['max_observed_rtt_ms']} ms",
        f"- Maximum observed tick duration: {summary['max_observed_tick_duration_seconds']} s",
        f"- Maximum sampled CPU: {summary['max_observed_cpu_percent_sample']}%",
        f"- Maximum sampled RSS: {summary['max_observed_rss_kb_sample']} KiB",
        "",
        "| Players | Repetition | Protocol | Tick overruns | Tick max (s) | RTT (ms) | CPU sample (%) | RSS sample (KiB) |",
        "|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    for record in records:
        lines.append(
            f"| {record['players']} | {record['repetition']} | {record['protocol_exit_code']} | "
            f"{record['tick_overruns']} | {record['tick_duration_seconds_max']} | "
            f"{record['rtt_ms']} | {record['cpu_percent_sample']} | {record['rss_kb_sample']} |"
        )
    report_md.write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(json.dumps({"run_id": run_id, "passed": passed, "report": str(report_md)}))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())

