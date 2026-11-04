#!/usr/bin/env python3
import datetime
import json
import os
import pathlib
import signal
import shlex
import subprocess
import sys
import time
import urllib.error
import urllib.request


ROOT = pathlib.Path(__file__).resolve().parents[2]


def request_json(base_url, method, path, token="", payload=None):
    body = None
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(base_url.rstrip("/") + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            raw = response.read()
            return response.status, json.loads(raw.decode("utf-8")) if raw else {}
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            detail = json.loads(raw.decode("utf-8"))
        except json.JSONDecodeError:
            detail = raw.decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} returned {error.code}: {detail}") from error


def wait_for_session(base_url, token, session_id, predicate, timeout=10):
    deadline = time.time() + timeout
    last = {}
    while time.time() < deadline:
        _, last = request_json(base_url, "GET", f"/v1/sessions/{session_id}", token)
        if predicate(last):
            return last
        time.sleep(0.25)
    raise RuntimeError(f"session did not reach expected state: {last}")


def stop_process(process):
    if process is None or process.poll() is not None:
        return
    process.send_signal(signal.SIGINT)
    try:
        process.wait(timeout=8)
    except subprocess.TimeoutExpired:
        process.terminate()
        process.wait(timeout=5)


def main():
    run_id = os.environ.get(
        "SESSION_AGENT_RUN_ID",
        datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ"),
    )
    run_dir = pathlib.Path(
        os.environ.get("SESSION_AGENT_OUTPUT_DIR", str(ROOT / "artifacts" / "session-agent" / run_id))
    )
    run_dir.mkdir(parents=True, exist_ok=True)
    api_base_url = os.environ.get("SESSION_AGENT_API_URL", "http://127.0.0.1:18081")
    listen_addr = os.environ.get("SESSION_AGENT_GAME_ADDR", "127.0.0.1:19200")
    health_addr = os.environ.get("SESSION_AGENT_HEALTH_ADDR", "127.0.0.1:19280")
    server_id = os.environ.get("ROS_GAME_SERVER_ID", "game-server-local")
    server_token = os.environ.get("ROS_GAME_SERVER_TOKEN", "phase11-game-server-token")
    game_command = shlex.split(
        os.environ.get("GAME_SERVER_CMD", str(ROOT / "bin" / "game-server"))
    )
    if not pathlib.Path(game_command[0]).exists():
        print("session-agent test requires a built game-server binary", file=sys.stderr)
        return 2

    password = "correct-horse-battery-staple"
    unique = f"{run_id.lower()}-{os.getpid()}"
    owner_email = f"agent-owner-{unique}@example.com"
    member_email = f"agent-member-{unique}@example.com"
    report = {
        "run_id": run_id,
        "api_base_url": api_base_url,
        "server_id": server_id,
        "passed": False,
        "steps": [],
    }
    server = None
    server_log_path = run_dir / "game-server.log"
    try:
        _, owner = request_json(
            api_base_url,
            "POST",
            "/v1/auth/register",
            payload={
                "email": owner_email,
                "password": password,
                "device_id": "session-agent-owner",
                "display_name": "session-agent-owner",
            },
        )
        _, member = request_json(
            api_base_url,
            "POST",
            "/v1/auth/register",
            payload={
                "email": member_email,
                "password": password,
                "device_id": "session-agent-member",
                "display_name": "session-agent-member",
            },
        )
        owner_token = owner["access_token"]
        member_token = member["access_token"]
        _, lobby = request_json(
            api_base_url,
            "POST",
            "/v1/lobbies",
            owner_token,
            {"mode": "duo", "region": "ap-southeast"},
        )
        lobby_id = lobby["id"]
        request_json(api_base_url, "POST", f"/v1/lobbies/{lobby_id}/join", member_token)
        request_json(api_base_url, "POST", f"/v1/lobbies/{lobby_id}/ready", owner_token, {"ready": True})
        request_json(api_base_url, "POST", f"/v1/lobbies/{lobby_id}/ready", member_token, {"ready": True})
        _, session = request_json(
            api_base_url,
            "POST",
            "/v1/sessions",
            owner_token,
            {"lobby_id": lobby_id, "max_players": 2},
        )
        session_id = session["id"]
        request_json(
            api_base_url,
            "POST",
            f"/v1/sessions/{session_id}/admit",
            owner_token,
            {"team_id": "team-agent"},
        )
        request_json(
            api_base_url,
            "POST",
            f"/v1/sessions/{session_id}/admit",
            member_token,
            {"team_id": "team-agent"},
        )
        report["steps"].append({"step": "session_created_and_admitted", "passed": True})

        environment = os.environ.copy()
        environment.update(
            {
                "ROS_GAME_LISTEN_ADDR": listen_addr,
                "ROS_GAME_HEALTH_ADDR": health_addr,
                "ROS_GAME_MATCH_DURATION": "60s",
                "ROS_GAME_STAGING_DURATION": "0s",
                "ROS_GAME_SERVER_ID": server_id,
                "ROS_GAME_SERVER_TOKEN": server_token,
                "ROS_API_BASE_URL": api_base_url,
                "ROS_GAME_SESSION_ID": session_id,
                "ROS_GAME_HEARTBEAT_INTERVAL": "1s",
            }
        )
        with server_log_path.open("w", encoding="utf-8") as server_log:
            server = subprocess.Popen(
                game_command,
                cwd=ROOT,
                env=environment,
                stdout=server_log,
                stderr=subprocess.STDOUT,
                text=True,
            )
            running = wait_for_session(
                api_base_url,
                owner_token,
                session_id,
                lambda value: value.get("status") == "running" and value.get("healthy") is True,
            )
        report["steps"].append(
            {
                "step": "game_server_started_and_heartbeat_healthy",
                "passed": True,
                "status": running.get("status"),
                "healthy": running.get("healthy"),
            }
        )
        stop_process(server)
        completed = wait_for_session(
            api_base_url,
            owner_token,
            session_id,
            lambda value: value.get("status") == "complete",
        )
        report["steps"].append(
            {
                "step": "graceful_game_server_completion",
                "passed": True,
                "status": completed.get("status"),
                "healthy": completed.get("healthy"),
            }
        )
        report["passed"] = True
    except Exception as error:
        report["error"] = str(error)
    finally:
        stop_process(server)

    report_path = run_dir / "session-agent-report.json"
    report_path.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"run_id": run_id, "passed": report["passed"], "report": str(report_path)}))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())

