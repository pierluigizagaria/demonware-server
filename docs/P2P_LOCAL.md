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

The child advertises the optional `owner.name.v1` capability in its `ready`
frame. A matching library may then send a private `owner.name` event containing
only the existing owner ID and a validated UTF-8 online ID. The publisher
updates the trusted authentication label, stored name and copied presence
snapshots without replacing credentials, changing identity/database keys or
disconnecting sessions. The exact original `osw1` capability remains valid;
its embedded initial name cannot roll back a trusted rename. Late tickets and
concurrent authentication cannot overwrite the current name in persistence or
presence. Existing ticket/session identity objects remain immutable; no opaque
game advertisement or room payload is rewritten to guess embedded names.
Unknown, unnegotiated, foreign-owner or malformed name frames close the pipe.
Older child versions advertise no capability; the library retains their
initial name until the context is restarted. Explicit configured names remain
fixed. This is a local replacement protocol, not a Steam-ticket claim.

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
using the actual observed source endpoint and bound publisher address unless
the optional virtual transport below is negotiated. Unknown
flags, periodic packets and introductions receive no fabricated response and
are never forwarded. Their diagnostics contain type/length only. Evidence:
workspace `docs/PS4_PEER_TRANSPORT.md` (C); local responder implementation (I).
The [paired-game NAT investigation](P2P_NAT_INVESTIGATION.md) records the
original classifier fallback and the later NAT-specific failure diagnostic.
It records the later original-game CommonAddr identity rejection and the
canonical endpoint correction. The CommonAddr NAT byte remains unchanged.

## Optional virtual endpoints

This server builds and tests independently of any host or platform library.
Production code uses only the inherited pipe contract; it does not link a
native host, load Steam, calculate canonical addresses or inspect game binaries.
The publisher owns CTR advertisement/CommonAddr validation and Demonware NAT
discovery. The library owns generic endpoint identity, authorization and opaque
peer transport. Each host owns its socket ABI and physical socket adaptation.

The optional owner field has this exact shape (synthetic example):

```json
{"transport":{"mode":"virtual-v1","address":"198.18.1.2","port":12345,"game_address":"127.0.0.11","game_port":3074}}
```

`address` and `port` are the library-supplied canonical IPv4 endpoint;
`game_address` and `game_port` are the actual local game's physical loopback
endpoint. Both ports must be nonzero unsigned 16-bit values. The descriptor is
immutable for that child. The publisher validates its shape and physical source
binding; address allocation and collision policy belong to the library.
This mode requires local admission and peer matchmaking. The publisher announces
`virtual.endpoints.v1` in `ready` only after configuring it; the library must
refuse an enabled virtual context if that capability is missing. An owner frame
without `transport` retains the explicit legacy ABI-2 behavior. Existing central
profiles do not enable this feature.

Only discovery packets from the exact configured game IP **and** port receive
the canonical endpoint in the observed-client field. Other loopback sources
retain their actual observed endpoint. The alternate server field remains the
physical bound publisher listener. Unknown discovery flags are still ignored;
no remote destination is opened or forwarded by this change.

In virtual mode `peer.endpoint` still authorizes/allocates the peer route, but
returns that peer's canonical endpoint. The publisher checks that the public
endpoint in the 67-byte CommonAddr matches it exactly, rejects the original
invalid-address sentinel, and preserves **all 67 bytes**. Local candidates may
legitimately use a different port; the host refuses unmapped candidates before
OS I/O. Own advertisements are checked against the inherited owner endpoint;
an incompatible update withdraws the private advertisement. A foreign or
unavailable endpoint does not produce a usable friend record or host document.
The complete accepted room host JSON, security ID/key, attributes and NAT byte
remain unchanged. Protected game datagrams are never parsed or rewritten here.

This is an explicit implementation choice (I), not physical NAT discovery or
Steam FakeIP. The original CommonAddr hash and MAC evidence is documented in
`P2P_NAT_INVESTIGATION.md`. Unit tests cover exact preservation, peer mismatch,
endpoint refusal, both IPv4 source representations and foreign-source isolation.
Optional real-library integration and owned-image instruction probes live under
`tools/p2p_probe/`; ordinary Go build/test does not require those dependencies.

## Companion lifecycle

The version-1 protocol is specified in the library's
`docs/COMPANION_PROTOCOL.md`. Frames are UTF-8 NDJSON, at most 98,304 bytes;
request/event/output queues are bounded. Operations have a 30-second bound.
Logs use stderr; stdout contains protocol only. An explicit `ready` event
follows storage and all listener startup. Stdin EOF initiates graceful shutdown.
Companion shutdown logs one fixed `reason` category distinguishing EOF, I/O,
frame/schema rejection, queue overflow and explicit/context cancellation.
The pipe records its first cause before signaling shutdown; later cleanup
cannot replace it. No frame contents, identity, credential, peer-provided
error string or underlying OS error is included. Earlier executables without
this diagnostic cannot distinguish these causes retrospectively.
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
An event observed during a membership query invalidates that query's snapshot;
selection refreshes it instead of overwriting the event. Three unstable snapshots
fail closed and leave the directory. This is a local revision guard, not an
inferred remote revision or wall-clock ordering.

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

Legacy owners without the virtual transport descriptor retain the earlier
all-eleven-endpoint document adapter. That path is not qualified for the original
game: the later identity investigation reproduced its CommonAddr rejection.
Virtual owners instead validate the authenticated public endpoint and preserve
the complete received host document, including every CommonAddr byte, original
host PID and DTLS data. The generic host socket adapter owns proxy routing.

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
address is validated against an authorized canonical peer endpoint and preserved
in virtual mode (legacy mode uses its earlier physical-proxy rewrite). Nothing is placed in public
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

Local authentication refusals log one fixed category (`unsupported-task`,
`malformed-envelope` or `owner-capability-mismatch`). They never log the task
value, credential or request body. These diagnostics do not add support for
renewal variants: the only admitted envelope remains the validated PS4 task 46.

The legacy-mode unit fixture compares create/join IDs, host/backend documents and
DTLS values against the unchanged central engine, allowing only the expected
recipient-specific proxy address. It also verifies reply/ack ordering, owner
departure, foreign-PID/nonmember refusal, unsupported operations, friend-record
serialization and local-only name resolution. These are implementation fixtures
(I), not live Steam or game acceptance. Two-PC Steam racing, game party/follower
propagation, invitations through the guest and race address embedding still
require their respective acceptance gates.

Lifecycle fixtures also compare a valid member cancel and a party follower's
departure against the central engine. A committed follower may never send a
room request; its authenticated directory departure must still release the
seat claimed by the leader. Cancelling the directory owner's waiting search
returns the same success reply as central cancellation, but closes the P2P
authority and discards its remaining searches. The central service survives
another player's cancellation. This difference is intentional and tested.

The notice comparison normalizes only the recipient's `listen_server.host_address`;
the host PID, every other host-document field and the complete backend document
must match the central engine. Distinct nonzero synthetic credentials exercise
preservation of the host's DTLS security ID and key. The follower-departure case
compares the complete owner update and the follower's empty notice sequence;
waiting member/owner cancellations require empty sequences for both recipients.
Fixture-only processing barriers drain generated cross-client events and check
that no requests or acknowledgement timers remain before asserting there are no
extra notices. These checks use no settling sleeps or production test hooks.
An empty-queue regression also verifies repeated `Events()` evaluations cannot
block independent consumer wakeups, and that a marker is acknowledged only
after delivery and consumer return, exactly once.
The targeted `TestP2P` tests and the same tests with Go's race detector pass on
Windows. The race detector required host execution because its memory allocation
failed inside the sandbox. These remain implementation evidence (I).

An additional Windows integration fixture starts two actual hidden publisher
children through the library's ABI 2 lab backend. It passes signed HTTP auth 46,
encrypted Demonware handshakes/tasks, pre-party 138/14 advertisements, service 12
names, a foreign-statistics empty result, owner create/member join and opaque
bidirectional UDP proxy delivery. Tokens exist only in process memory/stdin;
runtime keys, databases and child logs stay in ignored `.cache/`. This is still
simulated lab evidence (I), not a game race or two Steam accounts.
The reproducible source and invocation are in `tools/p2p_probe/README.md`.
The P2P, pipe, local admission, diagnostics and configuration packages also pass
Go's race detector with the existing project-local native Clang toolchain.
The probe's `--party` variant passes synthetic invitation/acceptance, committed
membership, a leader search claiming both members, follower room selection and
UDP through the previously advertised friend proxy. It exposed and verified a
library fix that promotes the existing socket into the shared room: the first
follower packet can then establish the owner's reverse endpoint using validated
membership. This does not establish how the real game propagates party state.
Both probe variants now disconnect the member's encrypted lobby connection and
require an owner document containing only the remaining seat. The party variant
exposed two independent defects: the publisher did not release a follower that
had never sent a room request, and the library immediately rejoined an explicitly
left selection. The publisher's directory-member diff and the library's explicit
leave suppression together pass this end-to-end implementation regression.

## Optional local timing diagnostics

`CTR_P2P_TIMING=1` enables timing only when `CTR_MATCHMAKING=steam`; it is off
by default. The publisher installs the sink before its P2P worker starts and
writes compact `[p2p-timing]` records to its existing private stderr log. It
does not enable payload dumps or change the pipe, peer protocol, deadlines,
matching engine, metadata policy or callback ownership.

Every record contains `utc_us`, `mono_us`, `seq`, `stage`, `op`, `code`, `phase`,
`elapsed_us` and `outcome`. A process-local diagnostic sequence pairs `start`
and `end`; it is unrelated to any account, connection, lobby or wire request.
UTC timestamps align local processes, while monotonic microseconds measure
durations. Operations, stages and outcomes are closed labels; task/event codes
are allowlisted. Unknown operation names become `none` and unknown codes become
zero. No arguments, results, addresses, identity fields or error text are logged.

- `call` measures companion operations, including directory selection/update,
  endpoint allocation and peer-send acknowledgement.
- `task` measures the local game task handler. For an existing `AfterReply`
  callback, `reply` ends when the router reports completion of the game reply
  write, then `after_reply` measures the original callback. A nil callback stays
  nil. Its outcome describes the input write error; the callback has no return
  value, so this is not a report of every operation it performs.
- `remote` measures the complete publisher-to-owner request/reply roundtrip;
  `peer.send` alone measures only the companion acknowledgement.
- `ack` measures from preparation of the owner's response to the existing game
  delivery ACK, timeout, cancellation or send failure. It does not activate work
  or alter the existing acknowledgement ownership.
- `push_prepare` measures document validation and endpoint preparation, then
  `push_write` reports the actual local game-socket write. A successful write
  does not establish that the game has processed the event or joined the lobby.

These timestamps complement existing player-info/search, CreateLobby (2005),
SyncDoc and connected-count logs. JoinLobby (2001) delivery is now observable
without enabling remote-peer identity logging. No shared tracing identifier is
added between processes; concurrent attempts cannot be paired across peers
solely by their local sequence numbers.

There is one synchronous write per record and no diagnostic queue, worker or
explicit flush. A start-record write is included in its span. The end timestamp
is taken before writing the end record, so that write is outside its own span
(and inside any enclosing span). Timing is therefore diagnostic evidence, not
a zero-overhead measurement. Behavioral fixtures compare identical tracing-off
and tracing-on task/push bytes, hold remote replies and game-delivery ACKs,
exercise failed delivery rollback, and verify that sensitive fixture markers
cannot enter records. They do not establish live game latency.

## Persona rename candidate validation (2026-10-16)

The `owner.name.v1` addition was built separately as
`bin/demonware-server-rename-candidate.exe`, SHA-256
`0fc70159dad030a978f7126e25368fec4e911cc8d418980f7268fe290a027369`, from
base `2d57a07464eb22a97c6cd71e8bd4614cb4780b73` plus the uncommitted rename
changes. The full `go test ./...` suite, `go vet ./...` and build passed.
Targeted `-race` tests for `internal/platform/local`, `internal/companion` and
`internal/presence` also passed on the host; the Windows sanitizer's allocation
failed inside the sandbox before test execution.

New regressions cover negotiated pipe rename before first auth 46, the original
capability after rename, invalid/unnegotiated/foreign-owner frames, immutable
identity keys and existing session copies, concurrent authentication and late
ticket registrations, and protection of current persisted/presence labels.
The matching isolated library candidate passed all 36 tests; its SHA-256 is
`508a135f34052108481c7427a3e8633b7d7b9a48794a071feef41ad2243e3786`.
No candidate was installed, no live persona was changed and no live game or
two-account acceptance is claimed. The earlier two-child probe evidence above
remains pinned to its earlier binaries.
