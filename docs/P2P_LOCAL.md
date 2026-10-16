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
Local mode requires log redaction and additionally withholds 64-bit identity,
session and connection identifiers. Raw captures cannot be enabled in this mode.
The host explicitly supplies `CTR_COMPANION=stdio`; the library itself uses the
title-independent marker `OSW_COMPANION_PROTOCOL=1`.

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

## Room engine and peer boundary

Set `CTR_MATCHMAKING=steam` to select the library directory. The existing
`Matchmaker` and merge implementation run on the directory owner's machine.
The adapter changes only service 145 routing; a remote peer has no handler for
economy, statistics, leaderboards, news, storage or other publisher services.
The room request allow-list is tasks 2, 8, 10, 13, 15 and 18 (player information,
cancel, leave, documents, sync and search). Registration/bootstrap replies stay
local. Unknown room operations fail closed. Task data is bounded to 16 KiB,
the complete room envelope to 32 KiB, with 120 messages/second per authenticated
member, 32 outbound pending requests and 64 pending acknowledgements.
Engine mutations and room invalidation are serialized. Closing a room cancels
pending member requests immediately, discards late activations, and rejects
older connection generations. A failed membership snapshot leaves the directory.

The version-1 peer envelope has a kind (`request`, `reply`, `ack`, `push`),
decimal-string sender PID and connection ID, request ID, task/event and base64
typed arguments. The library authenticates sender identity and membership;
the publisher additionally requires sender == claimed PID, current room
membership, owner-only replies/pushes and increasing request IDs per connection.
An acknowledgement after the member's local game reply preserves the engine's
existing reply-before-activation ordering. Each acknowledgement is bounded;
that bound applies to a single request, never an idle room or race session.

Directory metadata contains only a SHA-256 fingerprint of the existing search
filter (`playlist`), `type=ctr-async-1` and available slots.
Search results may omit members before joining, matching Steam visibility;
joined records must contain a validated owner and the local member. Original player
addresses and DTLS material travel only as member room messages. Party searches
may claim only current directory members; committed platform-party followers
are joined by the library and delivered as `room.selected`. Owner departure
invalidates the room; there is no automatic engine migration. Cross-directory
room merging is not implemented; the existing engine can merge only its own
rooms. Global simultaneous room-creation races may therefore form separate
rooms until players search again.

Received host documents and document replies replace all eleven CommonAddr
endpoints with the requesting player's local proxy endpoint for that host.
The NAT byte, original host PID and actual DTLS data are preserved. This is a
document adapter, not proof that race packets contain no embedded addresses.

## Friends and local privacy

Service 12 name lookups use the library's local Steam in-game friend cache.
They do not ask another publisher for identity or progression. Original
138/14 `Delta` queries occur before party membership, so party-only messaging
cannot satisfy the known friend-page flow (C: workspace
`docs/OFFICIAL_SHADNET_FRIENDS_TEST.md`, original result reader `0x401a00`).
The selected replacement (I) uses the library's dedicated friend advertisement
query channel. Only currently in-game, namespace-matching Steam friends may
read the owner's currently live 138 record; no remote writes are exposed.
The record is bounded to 4096 bytes, owner/context checked, and its common
address is rewritten to an authorized friend proxy. Nothing is placed in public
lobby data. Reserved, orphaned, stale-connection and deleted records are excluded.
No publication means an honest empty 138/14 result, not invented joinability.

Each local database is bound to one owner. Remote users are never registered in
local persistent player names, stats or economy. Existing local handlers return
only local data for cross-player reads; missing remote state remains absent.
The existing composed instance-isolation tests cover independent balances,
inventory, names and leaderboard stores; the P2P suite confirms no generic
publisher handler is reachable from peer room messages.

## Evidence status

Owner admission, configuration refusal, exact NAT shapes, pipe correlation and
EOF are fixture-tested (I). The complete existing Go suite passes on the Windows
host, including the unchanged dispatcher baseline. Sandboxed TCP fixtures hang
in the existing proxy-header test; host loopback execution passes. These results
do not establish native Online-menu, shared-room or race acceptance.

The two-publisher fixture compares create/join IDs, host/backend documents and
DTLS values against the unchanged central engine, allowing only the expected
recipient-specific proxy address. It also verifies reply/ack ordering, owner
departure, foreign-PID/nonmember refusal, unsupported operations, friend-record
serialization and local-only name resolution. These are implementation fixtures
(I), not live Steam or game acceptance. Two-PC Steam racing, game party/follower
propagation, invitations through the guest and race address embedding still
require their respective acceptance gates.

An additional Windows integration fixture starts two actual hidden publisher
children through the library's ABI 2 lab backend. It passes signed HTTP auth 46,
encrypted Demonware handshakes/tasks, pre-party 138/14 advertisements, service 12
names, a foreign-statistics empty result, owner create/member join and opaque
bidirectional UDP proxy delivery. Tokens exist only in process memory/stdin;
runtime keys, databases and child logs stay in ignored `.cache/`. This is still
simulated lab evidence (I), not a game race or two Steam accounts.
