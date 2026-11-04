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


def fetch(url):
    with urllib.request.urlopen(url, timeout=3) as response:
        return response.read().decode("utf-8")


def command_from_env(name, default):
    return shlex.split(os.environ.get(name, default))


def wait_ready(process, health_addr, log_path):
    url = f"http://{health_addr}/readyz"
    for _ in range(60):
        try:
            fetch(url)
            return True
        except (urllib.error.URLError, TimeoutError):
            if process.poll() is not None:
                return False
            time.sleep(0.25)
    return False


def run_protocol(command, environment, log_path, timeout=30):
    try:
        completed = subprocess.run(
            command,
            cwd=ROOT,
            env=environment,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
        output = completed.stdout + completed.stderr
        code = completed.returncode
    except subprocess.TimeoutExpired as error:
        output = (error.stdout or "") + (error.stderr or "") + "\ntimeout\n"
        code = 124
    log_path.write_text(output, encoding="utf-8")
    return code, output


def stop(process, force=False):
    if process is None or process.poll() is not None:
        return
    process.send_signal(signal.SIGKILL if force else signal.SIGTERM)
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()


def main():
    run_id = os.environ.get(
        "RECOVERY_RUN_ID",
        datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ"),
    )
    run_dir = pathlib.Path(
        os.environ.get("RECOVERY_OUTPUT_DIR", str(ROOT / "artifacts" / "recovery" / run_id))
    )
    run_dir.mkdir(parents=True, exist_ok=True)
    listen_addr = os.environ.get("RECOVERY_GAME_LISTEN_ADDR", "127.0.0.1:19100")
    health_addr = os.environ.get("RECOVERY_GAME_HEALTH_ADDR", "127.0.0.1:19180")
    game_command = command_from_env("GAME_SERVER_CMD", str(ROOT / "bin" / "game-server"))
    protocol_command = command_from_env("PROTOCOL_CLIENT_CMD", str(ROOT / "bin" / "protocol-client"))
    if not pathlib.Path(game_command[0]).exists() or not pathlib.Path(protocol_command[0]).exists():
        print("recovery test requires built game-server and protocol-client binaries", file=sys.stderr)
        return 2

    environment = os.environ.copy()
    environment.update(
        {
            "ROS_GAME_LISTEN_ADDR": listen_addr,
            "ROS_GAME_HEALTH_ADDR": health_addr,
            "ROS_GAME_MATCH_DURATION": os.environ.get("ROS_GAME_MATCH_DURATION", "60s"),
            "ROS_GAME_STAGING_DURATION": os.environ.get("ROS_GAME_STAGING_DURATION", "0s"),
            "ROS_GAME_ATTACK_COOLDOWN": os.environ.get("ROS_GAME_ATTACK_COOLDOWN", "1ms"),
        }
    )
    server_log = (run_dir / "game-server.log").open("w", encoding="utf-8")
    server = None
    records = []
    try:
        server = subprocess.Popen(
            game_command,
            cwd=ROOT,
            env=environment,
            stdout=server_log,
            stderr=subprocess.STDOUT,
            text=True,
        )
        initial_ready = wait_ready(server, health_addr, server_log.name)
        initial_env = environment.copy()
        initial_env.update(
            {
                "ROS_TEST_SUITE": "load",
                "ROS_TEST_PLAYERS": "2",
                "ROS_TEST_WAIT_MS": "1000",
                "ROS_GAME_SERVER_ADDR": listen_addr,
            }
        )
        initial_code, _ = run_protocol(
            protocol_command, initial_env, run_dir / "before-crash.log", timeout=30
        )
        records.append(
            {
                "stage": "before_crash",
                "ready": initial_ready,
                "protocol_exit_code": initial_code,
                "passed": initial_ready and initial_code == 0,
            }
        )
        stop(server, force=True)
        crashed_exit_code = server.returncode
        records.append(
            {
                "stage": "forced_crash",
                "exit_code": crashed_exit_code,
                "passed": crashed_exit_code is not None and crashed_exit_code != 0,
            }
        )
        server = subprocess.Popen(
            game_command,
            cwd=ROOT,
            env=environment,
            stdout=server_log,
            stderr=subprocess.STDOUT,
            text=True,
        )
        recovered_ready = wait_ready(server, health_addr, server_log.name)
        recovery_env = environment.copy()
        recovery_env.update(
            {
                "ROS_TEST_SUITE": "join_disconnect",
                "ROS_TEST_PLAYERS": "2",
                "ROS_TEST_WAIT_MS": "1000",
                "ROS_GAME_SERVER_ADDR": listen_addr,
            }
        )
        recovery_code, _ = run_protocol(
            protocol_command, recovery_env, run_dir / "after-recovery.log", timeout=30
        )
        records.append(
            {
                "stage": "after_recovery",
                "ready": recovered_ready,
                "protocol_exit_code": recovery_code,
                "passed": recovered_ready and recovery_code == 0,
            }
        )
    finally:
        stop(server)
        server_log.close()

    passed = bool(records) and all(record["passed"] for record in records)
    summary = {
        "run_id": run_id,
        "environment": "local independent game-server process",
        "forced_process_termination": True,
        "passed": passed,
        "records": records,
        "note": "This verifies process readiness and a new session after a forced restart; it does not claim in-flight match state survives a process crash.",
    }
    json_path = run_dir / "recovery-report.json"
    markdown_path = run_dir / "recovery-report.md"
    json_path.write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    markdown = [
        "# Game-server crash and recovery test",
        "",
        f"Run: {run_id}",
        "",
        "This test force-terminates the independent game-server process, starts it again on the same ports, and verifies readiness plus a new protocol session. It does not claim that in-flight match state survives a process crash.",
        "",
        f"Result: {'PASS' if passed else 'FAIL'}",
        "",
        "| Stage | Ready | Protocol exit code | Process exit code | Result |",
        "|---|---:|---:|---:|---|",
    ]
    for record in records:
        markdown.append(
            f"| {record['stage']} | {record.get('ready', '')} | "
            f"{record.get('protocol_exit_code', '')} | {record.get('exit_code', '')} | "
            f"{'PASS' if record['passed'] else 'FAIL'} |"
        )
    markdown_path.write_text("\n".join(markdown) + "\n", encoding="utf-8")
    print(json.dumps({"run_id": run_id, "passed": passed, "report": str(markdown_path)}))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())

