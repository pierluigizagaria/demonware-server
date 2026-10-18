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
import queue
import subprocess
import sys
import threading
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


class Endpoint(C.Structure):
    _fields_ = [("size", C.c_uint32), ("address", C.c_char * 16), ("port", C.c_uint32)]


def run():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--library", type=Path, required=True)
    parser.add_argument("--server", type=Path, required=True)
    parser.add_argument("--party", action="store_true", help="exercise a committed synthetic two-player party")
    parser.add_argument("--loopback", action="store_true", help="use loopback-v1 with no host endpoint translation")
    parser.add_argument("--weak-native-token", action="store_true", help="exercise explicit local fabricated-token admission")
    parser.add_argument("--mixed-economy", action="store_true", help="match normal and unlock-all publishers in one namespace")
    parser.add_argument("--app-id", type=int, default=480)
    parser.add_argument("--owned-identity-proof", action="store_true",
                        help="run the owned original receiver gate with the returned synthetic advertisement")
    parser.add_argument("--owned-image", type=Path)
    parser.add_argument("--image-inspector", type=Path)
    parser.add_argument("--rhash-library", type=Path)
    parser.add_argument("--go", type=Path, default=Path(".toolchain/go/bin/go.exe"))
    args = parser.parse_args()
    if not 1 <= args.app_id <= 0xffffffff:
        parser.error("--app-id must be a positive uint32")
    if args.owned_identity_proof and not all((args.owned_image, args.image_inspector, args.rhash_library)):
        parser.error("--owned-identity-proof requires --owned-image, --image-inspector and --rhash-library")
    if args.owned_identity_proof:
        args.owned_image = args.owned_image.resolve(strict=True)
        args.image_inspector = args.image_inspector.resolve(strict=True)
        args.rhash_library = args.rhash_library.resolve(strict=True)
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
    dll.osw_enable_virtual_endpoints.argtypes = [C.c_void_p]
    if args.loopback:
        dll.osw_enable_loopback_endpoints.argtypes = [C.c_void_p]
    if args.app_id != 480:
        dll.osw_set_app_id.argtypes = [C.c_void_p, C.c_uint32]
    dll.osw_get_local_endpoint.argtypes = [C.c_void_p, C.POINTER(Endpoint)]
    dll.osw_route_endpoint.argtypes = [C.c_void_p, C.c_uint32, C.POINTER(Endpoint), C.POINTER(Endpoint)]

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
            if args.weak_native_token:
                env["CTR_LOCAL_WEAK_TOKEN"] = "1"
            if args.mixed_economy and index == 1:
                env["CTR_UNLOCK_ALL"] = "1"
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
            enable_endpoints = dll.osw_enable_loopback_endpoints if args.loopback else dll.osw_enable_virtual_endpoints
            if enable_endpoints(ctx) != 0:
                raise RuntimeError("enable canonical endpoints failed")
            if args.app_id != 480 and dll.osw_set_app_id(ctx, args.app_id) != 0:
                raise RuntimeError("set app ID failed")
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
            if args.weak_native_token:
                token = f"sacredfire.{config.base.online_id.decode()}.{config.lab_account_id:016x}".encode()
            endpoint = Endpoint()
            endpoint.size = C.sizeof(endpoint)
            if dll.osw_get_local_endpoint(ctx, C.byref(endpoint)) != 0:
                raise RuntimeError("local canonical endpoint unavailable")
            inputs.append({"Token": token.decode(), "Address": address, "GameBind": game,
                           "Name": config.base.online_id.decode(), "PID": config.lab_account_id,
                           "PublicKey": base64.b64encode(public).decode(), "Party": args.party,
                           "Loopback": args.loopback, "WeakNativeToken": args.weak_native_token,
                           "MixedEconomy": args.mixed_economy,
                           "Index": index, "Canonical": {"Address":endpoint.address.decode(),"Port":endpoint.port}})
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
        # Inherited pipes bridge the synthetic game socket adapter to the same
        # generic C API that a host uses. Only final assertion records are shown.
        completed = subprocess.Popen([str(root / "client.exe")], stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                     text=True, bufsize=1)
        lines = queue.Queue()
        def read_client():
            try:
                for line in completed.stdout:
                    lines.put(line)
            finally:
                lines.put(None)
        reader = threading.Thread(target=read_client, daemon=True)
        reader.start()
        final = None
        proof_result = None
        try:
            completed.stdin.write(json.dumps(inputs) + "\n")
            completed.stdin.flush()
            deadline = time.monotonic() + 90
            while True:
                line = lines.get(timeout=max(.01, deadline - time.monotonic()))
                if line is None:
                    break
                message = json.loads(line)
                if not message.get("bridge"):
                    final = message
                    print(json.dumps(message, sort_keys=True))
                    continue
                if message["operation"] == "route":
                    if args.loopback:
                        raise RuntimeError("loopback fixture must not request host endpoint translation")
                    endpoint_in = Endpoint(C.sizeof(Endpoint), message["address"].encode(), message["port"])
                    endpoint_out = Endpoint()
                    endpoint_out.size = C.sizeof(endpoint_out)
                    status = dll.osw_route_endpoint(contexts[message["index"]], message["direction"],
                                                    C.byref(endpoint_in), C.byref(endpoint_out))
                    reply = {"Status":status,"Address":endpoint_out.address.decode(),"Port":endpoint_out.port}
                elif message["operation"] == "identity":
                    reply = {"OK":message["self"] == message["advertised"]}
                    if args.owned_identity_proof:
                        proof = subprocess.run([sys.executable,"-B",str(repo / "tools/p2p_probe/commonaddr_identity.py"),
                            "--image",str(args.owned_image),
                            "--inspector",str(args.image_inspector),
                            "--rhash",str(args.rhash_library),
                            "--documents-stdin"],input=json.dumps(message),text=True,capture_output=True,timeout=15)
                        proof_result = json.loads(proof.stdout) if proof.returncode == 0 else {"ok":False}
                        reply["OK"] = proof.returncode == 0 and proof_result.get("ok",False)
                        (root / "identity-proof.json").write_text(json.dumps(proof_result,indent=2)+"\n")
                else:
                    raise RuntimeError("unexpected fixture bridge operation")
                completed.stdin.write(json.dumps(reply) + "\n")
                completed.stdin.flush()
            completed.wait(timeout=max(.01,deadline-time.monotonic()))
        finally:
            if completed.poll() is None:
                completed.kill()
                completed.wait(timeout=5)
            completed.stdin.close()
            completed.stdout.close()
            reader.join(timeout=2)
        if proof_result is not None:
            print(json.dumps({"owned_identity_comparison":proof_result},sort_keys=True))
        if completed.returncode or final is None or not final.get("ok"):
            for index, ctx in enumerate(contexts):
                events, _ = call(ctx, {"op": "events.poll"})
                names = [event.get("event") for event in events.get("events", [])]
                print(json.dumps({"context_index": index, "room_selected_count": names.count("room.selected"),
                                  "room_changed_count": names.count("room.changed"),
                                  "room_closed_count": names.count("room.closed")}))
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
