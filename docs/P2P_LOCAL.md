# Local publisher and P2P boundary

Local mode runs this server as a supervised companion with private inherited
pipes. A compatible provider supplies identity, room discovery and opaque peer
transport. The server owns CTR admission, room messages and persistent data.
The pipe is a replacement integration protocol, not a Sony service. No particular
host or provider source tree is required.

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

`CTR_LOCAL_WEAK_TOKEN=1` is an explicit, default-off compatibility setting for
the unmodified native `online` host at `b1b4bf9`. It additionally accepts
`sacredfire.<name>.<16 lowercase hex account digits>` inside the unchanged PS4
task-46 envelope. This fabricated token proves no secret and is weaker than the
pipe capability: any local process that knows the owner's account ID can submit
it. It is allowed only with local admission and a literal loopback listener;
the HTTP handler also checks the actual socket source, ignoring forwarded
headers. The account suffix must equal the inherited pipe owner, while the name
never establishes identity or replaces the inherited label. Parse the suffix
from the right; printable ASCII names may contain dots, spaces and punctuation.
No new token service or remote admission route is introduced. Refusals use only
fixed categories `weak-token-malformed`, `weak-token-owner-mismatch`, and
`weak-token-nonloopback`, alongside the existing capability/envelope categories.

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

SQLite binds to the local namespace `ctrnf:local:v2:<backend>`.
Rows are keyed by SteamID, so one installation's database serves every
account, AppID and realm, and both economy modes: Lab and Steam identities still
never share a file. Unlock-all computes catalog ownership at read time and writes
no grant; race rewards, challenges and statistics are recorded in both modes.
A v1 local database (bound to one owner, AppID, realm and mode) is upgraded in
place in the transaction that validates its exact legacy binding; other bindings
are refused unchanged. The owner `app_id` is a positive numeric uint32 and is
mandatory for `loopback-v1`; absence or null is rejected rather than defaulted.
Independently selected legacy/virtual-v1 paths retain their existing default 480.
Owner, AppID, title, build and realm are still validated for every session.
Build changes do not reset the owner's progression; discovery matches the full
build namespace.
Economy policy is not part of the discovery namespace: normal and unlock-all
players use the same AppID/title/build/realm and may match together.
`CTR_ADMISSION=steam` retains the independent helper-based central admission.

`CTR_AUTH_HTTP=1` enables plain HTTP only on direct loopback. Auth and Umbrella
use the same auth listener; TLS files are not loaded in this mode. Replies are
unsigned; the independently validated game signature patch is required. With no new settings the existing HTTPS/central behavior stays intact.

Local NAT answers only the exact observed `1e 03 00` and `14 02 00 00` probes,
using the actual observed source endpoint and bound publisher address unless
the optional virtual transport below is negotiated. Unknown
flags, periodic packets and introductions receive no fabricated response and
are never forwarded. Their diagnostics contain type/length only.
The CommonAddr NAT byte remains unchanged.

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

This is a project transport contract, not physical NAT discovery or Steam FakeIP.
Stable endpoints preserve the CommonAddr identity used by the client; the
publisher never changes its hash, MAC or protected game datagrams.

### Loopback-global endpoints

The fixed-source `loopback-v1` descriptor has the same five fields as `virtual-v1`.
Its owner frame must explicitly carry `app_id`, including when the value is 480.
Its canonical endpoint is in the provider-reserved `127.64.0.0/10` range with a
port from 1024 through 65535. The game source must be a specific loopback IPv4
endpoint outside that reserved range; wildcard sources are not descriptors.
The publisher validates this contract without calculating addresses or opening
peer sockets. Only after installing the exact-source NAT mapping does it
acknowledge `loopback.endpoints.v1`; it does not acknowledge this mode as
`virtual.endpoints.v1`. Mixed modes fail the provider's capability negotiation.

Both canonical modes share the same CommonAddr checks and preserve all 67
bytes, complete host documents and original security material. In loopback
mode the provider binds remote proxies exactly at their canonical endpoints,
so an unmodified host sends directly to those local sockets. The publisher
still responds canonically only to the configured game IP and port. Other
sources retain actual-source NAT replies, and no race packet is rewritten.
Address allocation, collision handling, peer authorization and transport remain
provider responsibilities. Legacy and virtual defaults are unchanged.

A supervisor that observes the owned game's socket only after process launch
must explicitly select deferred binding before starting the provider. In that
case the owner transport has exactly four fields:

```json
{"mode":"loopback-v1","address":"127.64.1.2","port":12345,"game_pending":true}
```

`game_address` and `game_port` are absent, not guessed or set to wildcard values.
The publisher installs a silent discovery gate and advertises
`game.endpoint.v1` with `loopback.endpoints.v1`. The provider sends one private
`{"v":1,"event":"game.endpoint","id":"bind-1","address":"127.0.0.1","port":3104}`
frame after the supervisor explicitly binds its observed source through the
public C API. The publisher validates the source, commits the exact NAT mapping
and acknowledges the same ID/address/port with `event:"game.endpoint.bound"`.
Unknown fields, invalid sources, an unnegotiated event or any repeat close the
pipe with the fixed `game-endpoint` category. The callback and active NAT reader
synchronize the one-way transition; no packet is used to learn a source.

In deferred mode discovery is silent before commit, and stays silent for every
other source after commit. The provider withholds room, advertisement and peer
activity until the acknowledgement; the publisher also refuses outbound peer
operations while pending. Identity and platform friend WebAPI remain available.
The canonical endpoint and CommonAddr contract never change during the bind.
This is an explicit loopback mode; it adds no fallback to a missing descriptor
or capability and does not alter independent central/virtual operation.

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
cannot satisfy the friend-page flow (original result reader `0x401a00`).
The selected replacement (I) uses the library's dedicated friend advertisement
query channel. Only currently in-game, namespace-matching Steam friends may
read the owner's currently live 138 record; no remote writes are exposed.
The record is bounded to 4096 bytes, owner/context checked, and its common
address is validated against an authorized canonical peer endpoint and preserved
in virtual mode (legacy mode uses its earlier physical-proxy rewrite). Nothing is placed in public
lobby data. Reserved, orphaned, stale-connection and deleted records are excluded.
No publication means an honest empty 138/14 result, not invented joinability.

Each local database belongs to one installation. Remote users are never registered in
local persistent player names, stats or economy. Existing local handlers return
only local data for cross-player reads; missing remote state remains absent.
No generic publisher handler is reachable from peer room messages.

## Diagnostics

Local authentication refusals use fixed categories: `unsupported-task`,
`malformed-envelope` or `owner-capability-mismatch`. They never log credentials
or request bodies. Local admission accepts the PS4 task-46 envelope only.

`CTR_P2P_TIMING=1` adds compact timing records when `CTR_MATCHMAKING=steam`.
It is disabled by default and does not change protocol behavior or timeouts.
Records contain timestamps, a process-local sequence, stage, operation, code,
phase, duration and outcome. They contain no arguments, results, addresses or
identity fields. Each record is written synchronously to the private stderr
log, so tracing adds overhead. Sequence numbers do not correlate different
processes or prove that a game processed a delivered message.
