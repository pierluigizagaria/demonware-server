# Local publisher and P2P boundary

Decision: 2026-10-16. Replacement design (I), not a recovered Sony service.
The native CTR host loads orbiSteamworks and owns this publisher as a hidden
child. No hosting or distribution is implied. The original PolyForm Shield
license remains unchanged; the library and native host stay separate projects.

## Local admission

`CTR_ADMISSION=local` requires `CTR_COMPANION=stdio`, a literal loopback
`BIND_ADDR`, a loopback `NEXTENDO_HOST`, no PROXY protocol and no central UDP
relay. The library supplies one inherited-pipe owner frame within ten seconds.
It includes title `5760`, a bounded build namespace, realm, backend (`steam` or
`lab`), SteamID, UTF-8 online ID and a random per-child `osw1` capability. Only
that exact capability admits the owner through the original PS4 auth envelope.
The capability is a local credential, **not a validated Steam ticket**. It is
never sent to a remote admission helper or peer. Fresh publisher tickets retain
their existing connection-admission window; established sessions do not expire.

SQLite binds to a separate local namespace including backend, realm and owner.
Lab and Steam identities cannot share a database. Normal/unlock-all remain
fixed per instance and require separate databases and game saves. Build changes
do not reset the owner's progression; discovery matches the full build namespace.
`CTR_ADMISSION=steam` retains the independent helper-based central admission.

`CTR_AUTH_HTTP=1` enables plain HTTP only on direct loopback. Auth and Umbrella
use the same auth listener; TLS files are not loaded in this mode. Response
signing is still required, as is the independently validated game signature
patch. With no new settings the existing HTTPS/central behavior stays intact.

Local NAT answers only the exact observed `1e 03 00` and `14 02 00 00` probes,
using the actual observed source endpoint and bound publisher address. Unknown
flags, periodic packets and introductions receive no fabricated response and
are never forwarded. Their diagnostics contain type/length only. Evidence:
workspace `docs/PS4_PEER_TRANSPORT.md` (C); local responder implementation (I).

## Companion lifecycle

The version-1 protocol is specified in the library's
`docs/COMPANION_PROTOCOL.md`. Frames are UTF-8 NDJSON, at most 98,304 bytes;
request/event/output queues are bounded. Operations have a 30-second bound.
Logs use stderr; stdout contains protocol only. An explicit `ready` event
follows storage and all listener startup. Stdin EOF initiates graceful shutdown.
The native parent separately owns the child through its Windows Job Object.

Minimal P1 environment (all paths are private, operator supplied):

```dotenv
CTR_ADMISSION=local
CTR_COMPANION=stdio
CTR_MATCHMAKING=central
CTR_AUTH_HTTP=1
BIND_ADDR=127.0.0.10
NEXTENDO_HOST=127.0.0.10
AUTH_PORT=8460
LOBBY_PORTS=3074
NAT_PORT=3074
DASH_PORT=8093
CTR_AUTH_SIGNING_KEY=private-response-key.pem
DATA_DIR=private-normal-state
```

The game's bind address must differ from the publisher's address (for example
`127.0.0.11:3074` versus `127.0.0.10:3074`). No system firewall, hosts or network
settings are changed. Existing publisher/economy functions remain local.

## Evidence status

Owner admission, configuration refusal, exact NAT shapes, pipe correlation and
EOF are fixture-tested (S). The complete existing Go suite passes on the Windows
host, including the unchanged dispatcher baseline. Sandboxed TCP fixtures hang
in the existing proxy-header test; host loopback execution passes. These results
do not establish native Online-menu, shared-room or race acceptance.
