"""Optional Windows two-publisher integration fixture; never a live game test.

Requires a built orbiSteamworks ABI 2 DLL, publisher, local Go, and OpenSSL.
All identities are synthetic. Tokens stay in memory and child stdin. Keys,
databases, binaries and logs are retained only under the ignored .cache folder.
"""

import argparse
import base64
import ctypes as C
import json
import os
from pathlib import Path
import subprocess
import time
import uuid


class Config(C.Structure):
    _fields_ = [("size", C.c_uint32), ("abi_version", C.c_uint32),
                ("library_path", C.c_char * 1024), ("online_id", C.c_char * 17),
                ("title", C.c_char * 17), ("build", C.c_char * 65), ("realm", C.c_char * 65)]


class Config2(C.Structure):
    _fields_ = [("base", Config), ("backend", C.c_uint32), ("game_port", C.c_uint32),
                ("lab_account_id", C.c_uint64), ("lab_root", C.c_char * 1024),
                ("companion_path", C.c_char * 1024), ("companion_working_dir", C.c_char * 1024),
                ("companion_environment", C.c_char * 8192), ("game_bind", C.c_char * 16)]


class Request(C.Structure):
    _fields_ = [("size", C.c_uint32), ("operation", C.c_uint32), ("timeout_ms", C.c_uint32),
                ("metadata", C.c_char_p), ("metadata_size", C.c_size_t),
                ("body", C.c_void_p), ("body_size", C.c_size_t)]


class Result(C.Structure):
    _fields_ = [("size", C.c_uint32), ("status", C.c_int32), ("http_status", C.c_int32),
                ("metadata", C.c_void_p), ("metadata_size", C.c_size_t),
                ("body", C.c_void_p), ("body_size", C.c_size_t)]


class Event(C.Structure):
    _fields_ = [("size", C.c_uint32), ("type", C.c_uint32),
                ("request_id", C.c_uint64), ("account_id", C.c_uint64),
                ("session_id", C.c_char * 32), ("invitation_id", C.c_char * 65),
                ("online_id", C.c_char * 17), ("change_kind", C.c_uint32)]


def run():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--library", type=Path, required=True)
    parser.add_argument("--server", type=Path, required=True)
    parser.add_argument("--party", action="store_true", help="exercise a committed synthetic two-player party")
    parser.add_argument("--go", type=Path, default=Path(".toolchain/go/bin/go.exe"))
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[2]
    root = repo / ".cache" / "p2p-probe" / uuid.uuid4().hex
    root.mkdir(parents=True)
    server, library = args.server.resolve(strict=True), args.library.resolve(strict=True)
    environment = os.environ.copy()
    environment["GOCACHE"] = str(repo / ".cache" / "go-build")
    environment["GOPROXY"] = "off"
    subprocess.run([str(args.go.resolve(strict=True)), "build", "-o", str(root / "client.exe"),
                    "./tools/p2p_probe/client.go"], cwd=repo, env=environment, check=True)
    subprocess.run(["openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048",
                    "-out", str(root / "response.pem")], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    public = subprocess.run(["openssl", "pkey", "-in", str(root / "response.pem"),
                             "-pubout", "-outform", "DER"], check=True,
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL).stdout
    dll = C.CDLL(str(library))
    dll.osw_create_v2.argtypes = [C.POINTER(Config2), C.POINTER(C.c_void_p)]
    dll.osw_start.argtypes = [C.c_void_p]
    dll.osw_destroy.argtypes = [C.c_void_p]
    dll.osw_destroy.restype = None
    dll.osw_submit.argtypes = [C.c_void_p, C.POINTER(Request), C.POINTER(C.c_uint64)]
    dll.osw_get_result.argtypes = [C.c_void_p, C.c_uint64, C.POINTER(Result)]
    dll.osw_release.argtypes = [C.c_void_p, C.c_uint64]
    dll.osw_poll_event.argtypes = [C.c_void_p, C.POINTER(Event)]

    def call(ctx, metadata, operation=8, body=b""):
        raw = json.dumps(metadata).encode()
        buffer = C.create_string_buffer(body)
        request = Request(C.sizeof(Request), operation, 30000, raw, len(raw), C.cast(buffer, C.c_void_p), len(body))
        ident = C.c_uint64()
        if dll.osw_submit(ctx, C.byref(request), C.byref(ident)) != 0:
            raise RuntimeError("submit failed")
        result = Result()
        result.size = C.sizeof(result)
        until = time.monotonic() + 35
        try:
            while True:
                status = dll.osw_get_result(ctx, ident, C.byref(result))
                if status == 0:
                    break
                if status != 1:
                    raise RuntimeError("result lookup failed")
                if time.monotonic() > until:
                    raise RuntimeError("result timeout")
                time.sleep(.01)
            if result.status != 0:
                raise RuntimeError("operation refused")
            return (json.loads(C.string_at(result.metadata, result.metadata_size) or b"{}"),
                    C.string_at(result.body, result.body_size) if result.body else b"")
        finally:
            dll.osw_release(ctx, ident)

    contexts, inputs = [], []
    try:
        for index, (address, game) in enumerate([("127.0.0.60", "127.0.0.61"),
                                                 ("127.0.0.70", "127.0.0.71")]):
            state = root / ("player" + str(index))
            state.mkdir()
            env = {"CTR_ADMISSION": "local", "CTR_COMPANION": "stdio", "CTR_MATCHMAKING": "steam",
                   "CTR_AUTH_HTTP": "1", "BIND_ADDR": address, "NEXTENDO_HOST": address,
                   "AUTH_PORT": "8460", "LOBBY_PORTS": "3074", "NAT_PORT": "3074", "DASH_PORT": "8093",
                   "CTR_AUTH_SIGNING_KEY": str(root / "response.pem"), "DATA_DIR": str(state),
                   "OSW_COMPANION_STDERR": str(state / "child.log")}
            config = Config2()
            config.base.size, config.base.abi_version = C.sizeof(config), 2
            config.base.online_id = b"LabOne" if index == 0 else b"LabTwo"
            config.base.title, config.base.build, config.base.realm = b"5760", b"01.21", b"publisher-probe"
            config.backend, config.game_port, config.lab_account_id = 1, 3074, 100001 + index
            config.lab_root = str(root / "lab").encode()
            config.companion_path = str(server).encode()
            config.companion_working_dir = str(state).encode()
            config.companion_environment = json.dumps(env).encode()
            config.game_bind = game.encode()
            ctx = C.c_void_p()
            if dll.osw_create_v2(C.byref(config), C.byref(ctx)) != 0:
                raise RuntimeError("create failed")
            contexts.append(ctx)
            if dll.osw_start(ctx) != 0:
                raise RuntimeError("start failed")
            until = time.monotonic() + 15
            while True:
                try:
                    status, _ = call(ctx, {"op": "status"})
                except RuntimeError:
                    status = {}
                if status.get("ready"):
                    break
                if time.monotonic() > until:
                    raise RuntimeError("publisher readiness")
                time.sleep(.05)
            _, token = call(ctx, {"client_id": "publisher-probe", "scope": "openid"}, 1)
            inputs.append({"Token": token.decode(), "Address": address, "GameBind": game,
                           "Name": config.base.online_id.decode(), "PID": config.lab_account_id,
                           "PublicKey": base64.b64encode(public).decode(), "Party": args.party})
        until = time.monotonic() + 5
        while True:
            if all(len(call(ctx, {"op": "name.lookup", "names": ["LabOne", "LabTwo"]})[0]
                       .get("players", [])) == 1 for ctx in contexts):
                break
            if time.monotonic() > until:
                raise RuntimeError("friend presence readiness")
            time.sleep(.1)
        if args.party:
            description = {"sessionType": "owner-bind", "sessionPrivacy": "private", "sessionMaxUser": 8,
                           "sessionLockFlag": False, "availablePlatforms": ["PS4"]}
            def part(name, value):
                return ("--fixture\r\nContent-Type: application/octet-stream\r\n"
                        "Content-Disposition: attachment; name=\"" + name + "\"\r\n\r\n" + value + "\r\n")
            body = (part("session-request", json.dumps(description)) + part("session-image", "image") +
                    part("session-data", "data") + part("changeable-session-data", "change") + "--fixture--\r\n").encode()
            _, response = call(contexts[0], {"method": "POST", "path": "/v1/sessions",
                                            "content_type": "multipart/mixed; boundary=fixture"}, 2, body)
            session = json.loads(response)["sessionId"]
            call(contexts[0], {"session_id": session, "to": ["LabTwo"]}, 4)
            event = Event()
            until = time.monotonic() + 5
            while True:
                event.size = C.sizeof(event)
                if dll.osw_poll_event(contexts[1], C.byref(event)) == 0 and event.type == 4:
                    break
                if time.monotonic() > until:
                    raise RuntimeError("synthetic invitation event missing")
                time.sleep(.01)
            call(contexts[1], {"invitation_id": event.invitation_id.decode()}, 5)
            call(contexts[1], {"method": "POST", "path": "/v1/sessions/" + session + "/members",
                               "content_type": "application/json"}, 2, b"{}")
            time.sleep(.1)  # bounded fixture synchronization for committed membership
        completed = subprocess.run([str(root / "client.exe")], input=json.dumps(inputs),
                                   text=True, capture_output=True, timeout=90)
        # The helper emits only fixed step names and aggregate booleans.
        print(completed.stdout.strip())
        if completed.returncode:
            raise RuntimeError("publisher engine integration failed")
        if args.party:
            events, _ = call(contexts[1], {"op": "events.poll"})
            if not any(event.get("event") == "room.selected" for event in events.get("events", [])):
                raise RuntimeError("committed follower did not receive room selection")
            print(json.dumps({"ok": True, "simulated_invite_accept": True, "follower_room_selected": True}))
    finally:
        for ctx in reversed(contexts):
            dll.osw_destroy(ctx)


if __name__ == "__main__":
    run()
