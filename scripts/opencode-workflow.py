"""Exercise Stage HTTP, CLI and native MCP against an isolated OpenCode V2 service."""

import base64
import contextlib
import json
import os
import pathlib
import signal
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

binary = pathlib.Path.cwd() / "bin/stage"


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def until(fn, desc, timeout=50):
    stop = time.monotonic() + timeout
    last = None
    while time.monotonic() < stop:
        try:
            result = fn()
            if result:
                return result
            last = result
        except Exception as e:
            last = e
        time.sleep(0.2)
    raise RuntimeError(f"{desc}: {last!r}")


def request(base, path, body=None, method=None, auth=False):
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if auth:
        headers["Authorization"] = "Basic " + base64.b64encode(b"opencode:fixture").decode()
    req = urllib.request.Request(
        base + path,
        headers=headers,
        data=json.dumps(body).encode() if body is not None else None,
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            b = r.read()
            return json.loads(b) if b else None
    except urllib.error.HTTPError as e:
        raise ValueError((e.code, e.read().decode())) from e


class Model(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        data = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        names = [v["function"]["name"] for v in data.get("tools", [])]
        called = any(m["role"] == "tool" for m in data["messages"])
        tool = next((n for n in names if n.endswith("create_task")), None)
        call = tool and not called and "exercise native context" in str(data["messages"])
        delta = {"role": "assistant", "content": "Verified local fixture reply"}
        if call:
            args = {
                "title": "Native agent child",
                "prompt": "Retain my origin",
                "initialNote": "Created inside OpenCode",
            }
            delta = {
                "role": "assistant",
                "tool_calls": [
                    {
                        "index": 0,
                        "id": "call_native",
                        "type": "function",
                        "function": {"name": tool, "arguments": json.dumps(args)},
                    }
                ],
            }

        def chunk(delta, reason):
            return (
                "data: "
                + json.dumps(
                    {
                        "id": "one",
                        "object": "chat.completion.chunk",
                        "created": 1,
                        "model": "test",
                        "choices": [{"index": 0, "delta": delta, "finish_reason": reason}],
                    }
                )
                + "\n\n"
            )

        body = (
            chunk(delta, None) + chunk({}, "tool_calls" if call else "stop") + "data: [DONE]\n\n"
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


with tempfile.TemporaryDirectory(prefix="stage-workflow-") as root, contextlib.ExitStack() as cleanup:
    root = pathlib.Path(root)
    repo = root / "repo"
    repo.mkdir()
    env = {"HOME": str(root), "PATH": os.environ["PATH"], "XDG_RUNTIME_DIR": str(root), "TMPDIR": str(root)}
    subprocess.run(["git", "init", "-q"], cwd=repo, env=env, check=True)
    subprocess.run(
        [
            "git",
            "-c",
            "user.name=fixture",
            "-c",
            "user.email=fixture@invalid",
            "commit",
            "-qm",
            "fixture",
            "--allow-empty",
        ],
        cwd=repo,
        env=env,
        check=True,
    )
    model = ThreadingHTTPServer(("127.0.0.1", 0), Model)
    threading.Thread(target=model.serve_forever, daemon=True).start()
    cleanup.callback(model.server_close)
    cleanup.callback(model.shutdown)
    stage_port, oc_port, absent_t3_port = port(), port(), port()
    stage = f"http://127.0.0.1:{stage_port}"
    oc = f"http://127.0.0.1:{oc_port}"

    def launch(args, environ):
        p = subprocess.Popen(
            args,
            cwd=repo,
            env=environ,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )

        def stop():
            if p.poll() is None:
                os.killpg(p.pid, signal.SIGTERM)
                try:
                    p.wait(timeout=12)
                except subprocess.TimeoutExpired:
                    os.killpg(p.pid, signal.SIGKILL)
                    p.wait()

        cleanup.callback(stop)
        return stop

    stage_env = env | {
        "STAGE_DATA_DIR": str(root / "data"),
        "T3_URL": f"http://127.0.0.1:{absent_t3_port}",
        "STAGE_LISTEN": f"127.0.0.1:{stage_port}",
    }

    def start_stage():
        stop = launch([binary, "serve"], stage_env)
        until(lambda: request(stage, "/api/health"), "stage readiness")
        return stop

    stop_stage = start_stage()
    cfg = {
        "enabled_providers": ["fixture"],
        "model": "fixture/test",
        "provider": {
            "fixture": {
                "npm": "@ai-sdk/openai-compatible",
                "name": "fixture",
                "options": {"baseURL": f"http://127.0.0.1:{model.server_port}/v1", "apiKey": "fixture"},
                "models": {"test": {"name": "Fixture"}},
            }
        },
        "mcp": {
            "servers": {
                "stage": {
                    "type": "remote",
                    "url": stage + "/mcp",
                    "oauth": False,
                    "codemode": False,
                    "protocol": "legacy",
                }
            }
        },
    }
    launch(
        [os.environ["STAGE_TEST_OPENCODE"], "serve", "--hostname", "127.0.0.1", "--port", str(oc_port)],
        env
        | {
            "OPENCODE_PASSWORD": "fixture",
            "OPENCODE_DISABLE_AUTOUPDATE": "1",
            "OPENCODE_DISABLE_MODELS_FETCH": "1",
            "OPENCODE_DISABLE_FREE_MODELS": "1",
            "OPENCODE_CONFIG_CONTENT": json.dumps(cfg),
        },
    )
    until(
        lambda: request(oc, "/api/location?directory=" + urllib.parse.quote(str(repo)), auth=True),
        "OpenCode location",
    )
    cid = request(stage, "/api/opencode/status")["connectionId"]
    client = "http://localhost:" + str(oc_port)
    request(
        stage,
        "/api/opencode/settings",
        {
            "endpoint": oc,
            "browserEndpoint": oc,
            "webClient": client,
            "username": "opencode",
            "password": "fixture",
        },
        "PUT",
    )
    status = request(stage, "/api/opencode/status")
    assert status["state"] == "connected" and status["hasCredentials"] and "fixture" not in json.dumps(status)
    synced = request(stage, "/api/sync", {})
    assert synced["projects"] >= 1 and synced["unavailableConnections"] == [1], synced
    project = request(stage, "/api/projects", {"title": "Integration fixture"})
    pid = project["id"]
    inventory = request(stage, f"/api/connections/{cid}/projects")["projects"]
    workspace = next(w for w in inventory if w["workspace"] == str(repo))
    request(
        stage,
        f"/api/projects/{pid}/binding",
        {"connectionId": cid, "externalProjectId": workspace["externalId"], "workspace": str(repo)},
        "PUT",
    )

    def cli(*args):
        return json.loads(
            subprocess.check_output([binary, *args, "--url", stage, "--json"], env=env, cwd=repo)
        )

    assert "fixture/test" in str(cli("models", "ls", "--connection", str(cid), "--project", str(pid)))
    try:
        request(
            stage,
            "/api/tasks",
            {
                "projectId": pid,
                "connectionId": cid,
                "title": "Unsupported selection",
                "prompt": "Exercise validation",
                "executionConfig": {"model": "fixture/missing"},
            },
        )
    except ValueError as e:
        assert "model" in str(e).lower() and "fixture/missing" in str(e), e
    else:
        raise AssertionError("accepted an unavailable model")
    task = cli(
        "task",
        "add",
        "--project",
        str(pid),
        "--title",
        "Native parent",
        "--prompt",
        "exercise native context",
        "--start",
    )
    tid = task["id"]
    parent = until(
        lambda: t if (t := request(stage, f"/api/tasks/{tid}"))["state"] == "done" else None,
        "native task finish",
    )
    assert (
        parent["acceptanceId"] is None and parent["finalReply"]["text"] == "Verified local fixture reply"
    ), parent
    result = request(stage, "/api/tasks")["tasks"]
    child = next((t for t in result if t["title"] == "Native agent child"), None)
    assert child and child["originSession"] == parent["session"], (child, parent["session"])
    journal = request(stage, f"/api/tasks/{child['id']}/journal")["entries"]
    assert any(
        n["sourceSession"] == parent["session"] and n["text"] == "Created inside OpenCode" for n in journal
    ), journal
    assert parent["links"]["web"].startswith(client + "/server/") and parent["links"]["service"].startswith(
        oc + "/server/"
    )
    print("Native OpenCode tool call created task and journal with exact caller", flush=True)
    external = request(
        oc,
        "/api/session",
        {"title": "Adopted outside Stage", "location": {"directory": str(repo)}},
        auth=True,
    )["data"]["id"]
    sref = f"/api/connections/{cid}/sessions/{external}"
    preview = request(stage, sref + "/context")
    assert preview["project"]["id"] == pid and preview["task"] is None
    attached = request(stage, sref + "/adopt", {"initialNote": "Explicitly adopted"})
    assert attached["state"] == "backlog" and attached["adopted"]
    sc = cli(
        "schedule",
        "add",
        "--project",
        str(pid),
        "--connection",
        str(cid),
        "--title",
        "Recurring fixture",
        "--prompt",
        "Scheduled isolated work",
        "--cron",
        "* * * * *",
        "--tz",
        "UTC",
    )

    def occurrence():
        runs = request(stage, f"/api/schedules/{sc['id']}")["runs"]
        return next((r["task"] for r in runs if r.get("task") and r["task"]["state"] == "done"), None)

    fired = until(occurrence, "clock-driven occurrence", 105)
    request(stage, f"/api/schedules/{sc['id']}", {"paused": True}, "PATCH")
    stop_stage()
    start_stage()
    after = request(stage, f"/api/tasks/{fired['id']}")
    assert (
        after["session"] == fired["session"] and after["acceptanceId"] is None and after["launchAttempt"] == 1
    )
    print("OpenCode-only setup, explicit adoption, scheduled run and Stage restart passed", flush=True)
