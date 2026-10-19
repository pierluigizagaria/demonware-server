# Local publisher and P2P boundary

In local mode this server runs as a supervised companion of a platform provider,
connected through private inherited pipes. The provider, such as
[orbiSteamworks](https://github.com/pierluigizagaria/orbisteamworks), supplies
identity, room discovery and opaque peer transport; this server owns CTR
admission, room messages and persistent data. The pipe protocol is defined by
these projects and is not a Sony service. No host or provider source tree is
required to build or test this server.

## Local admission

`CTR_ADMISSION=local` requires `CTR_COMPANION=stdio`, a literal loopback
`BIND_ADDR`, a loopback `NEXTENDO_HOST`, no PROXY protocol and no central UDP
relay. The provider sends one owner frame over the pipe within ten seconds. It
includes title `5760`, a bounded build namespace, realm, backend (`steam`, or
`lab` for the provider's simulated test backend), SteamID, UTF-8 online ID and
a random per-process `osw1` capability. Only that exact capability admits the
owner through the original PS4 authentication envelope. The capability is a
local credential, **not a validated Steam ticket**, and is never sent to a
remote admission helper or peer. Fresh lobby tickets keep the normal
connection-admission window; established sessions do not expire.

`CTR_LOCAL_WEAK_TOKEN=1` is an explicit, default-off compatibility setting for
the unmodified GetLostIn native port (`online` revision `b1b4bf9`). It also
accepts `sacredfire.<name>.<16 lowercase hex account digits>` inside the PS4
task-46 envelope. This fabricated token proves no secret and is weaker than the
pipe capability: any local process that knows the owner's account ID can submit
it. It is accepted only with local admission and a literal loopback listener,
and the HTTP handler checks the actual socket source, ignoring forwarded
headers. The account suffix must equal the pipe owner; the name never
establishes identity or replaces the owner's name. The suffix is parsed from the
right, because printable ASCII names may contain dots, spaces and punctuation.
Refusals use the fixed categories `weak-token-malformed`,
`weak-token-owner-mismatch` and `weak-token-nonloopback`.

### Owner name updates

The server advertises the optional `owner.name.v1` capability in its `ready`
frame. The provider may then send a private `owner.name` event containing only
the owner ID and a validated UTF-8 online ID. The server updates the trusted
authentication label, stored name and copied presence snapshots without
replacing credentials, changing identity or database keys, or disconnecting
sessions. The original `osw1` capability remains valid; the initial name
embedded in it cannot roll back a rename. Late tickets and concurrent
authentication cannot overwrite the current name in persistence or presence.
Ticket and session identity objects are immutable, and no game advertisement or
room payload is rewritten to replace embedded names. Unknown, unnegotiated,
foreign-owner or malformed name frames close the pipe. A configured explicit name
stays fixed.

### Storage and namespace

SQLite binds to the local namespace `ctrnf:local:v2:<backend>`. Rows are keyed by
SteamID, so one installation's database serves every account, AppID and realm
and both economy modes; databases of the Steam and simulated test backends are
never shared. Unlock-all computes catalog ownership at read time and writes no
grant; race rewards, challenges and statistics are recorded in both modes.
Databases with another binding are refused unchanged.

The owner `app_id` is a positive uint32 and is mandatory for `loopback-v1`;
absence or null is rejected rather than defaulted. In the default and
`virtual-v1` modes an absent `app_id` means 480. Owner, AppID, title, build and realm are
validated for every session. Build changes do not reset the owner's progression,
and discovery matches the full build namespace. The economy mode is not part of
the discovery namespace: normal and unlock-all players use the same
AppID/title/build/realm and can match together.

`CTR_AUTH_HTTP=1` enables plain HTTP only on direct loopback. Auth and Umbrella
use the same auth listener, and TLS files are not loaded in this mode. Replies
are unsigned; the game signature patch is required.

### NAT discovery

Local NAT answers only the `1e 03 00` and `14 02 00 00` discovery packets
observed from the game, using the actual source endpoint and the bound server
address unless an endpoint mode below is negotiated. Unknown flags, periodic
packets and introductions receive no fabricated response and are never
forwarded; their diagnostics contain type and length only. The CommonAddr NAT
byte is preserved.

## Endpoint modes

Production code uses only the pipe contract: it does not link a native host,
load Steam, calculate canonical addresses or inspect game binaries. The server
owns CTR advertisement and CommonAddr validation and Demonware NAT discovery.
The provider owns endpoint identity, authorization and opaque peer transport,
and each host owns its own socket adaptation.

### `virtual-v1`

The optional owner field has this exact shape (example values):

```json
{"transport":{"mode":"virtual-v1","address":"198.18.1.2","port":12345,"game_address":"127.0.0.11","game_port":3074}}
```

`address` and `port` are the provider's canonical IPv4 endpoint; `game_address`
and `game_port` are the local game's physical loopback endpoint. Both ports must
be nonzero 16-bit values, and the descriptor is fixed for the process. The server
validates its shape and the physical source binding; address allocation and
collision policy belong to the provider. This mode requires local admission and
peer matchmaking. The server announces `virtual.endpoints.v1` in `ready` only
after configuring it, and the provider must refuse a virtual context without that
capability. An owner frame without `transport` uses the default mode.

Only discovery packets from the exact configured game IP **and** port receive the
canonical endpoint in the observed-client field; other loopback sources keep their
actual observed endpoint. The alternate server field remains the physical server
listener. Unknown discovery flags are ignored, and no remote destination is
opened or forwarded.

In this mode `peer.endpoint` authorizes and allocates the peer route but returns
that peer's canonical endpoint. The server checks that the public endpoint in the
67-byte CommonAddr matches it exactly, rejects the original invalid-address
sentinel and preserves **all 67 bytes**. Local candidates may use a different
port; the host refuses unmapped candidates before OS I/O. The server's own
advertisements are checked against the owner endpoint, and an incompatible
update withdraws the private advertisement. A foreign or unavailable endpoint
does not produce a usable friend record or host document. The accepted room host
JSON, security ID and key, attributes and NAT byte are passed through unchanged,
and protected game datagrams are never parsed or rewritten.

Canonical endpoints are a transport contract between these projects, not
physical NAT discovery or Steam FakeIP. Stable endpoints preserve the CommonAddr
identity the client derives from them; the server never changes its hash, MAC or
protected game datagrams.

### `loopback-v1`

The fixed-source `loopback-v1` descriptor has the same five fields. Its owner
frame must carry `app_id`, including when the value is 480. The canonical
endpoint is in the provider-reserved `127.64.0.0/10` range with a port from 1024
through 65535. The game source must be a specific loopback IPv4 endpoint outside
that range; wildcard sources are refused. The server validates this contract
without calculating addresses or opening peer sockets, and acknowledges
`loopback.endpoints.v1` (not `virtual.endpoints.v1`) only after installing the
exact-source NAT mapping. Mixed modes fail the provider's capability negotiation.

Both endpoint modes share the CommonAddr checks and preserve all 67 bytes,
complete host documents and original security material. In loopback mode the
provider binds remote proxies exactly at their canonical endpoints, so an
unmodified host sends directly to those local sockets. The server responds
canonically only to the configured game IP and port; other sources get
actual-source NAT replies, and no game datagram is rewritten.

### Deferred game source

A supervisor that only learns the game's socket after launch selects deferred
binding before starting the provider. The owner transport then has exactly four
fields:

```json
{"mode":"loopback-v1","address":"127.64.1.2","port":12345,"game_pending":true}
```

`game_address` and `game_port` are absent. The server installs a silent discovery
gate and advertises `game.endpoint.v1` with `loopback.endpoints.v1`. After the
supervisor binds its observed source through the provider's C API, the provider
sends one frame such as
`{"v":1,"event":"game.endpoint","id":"bind-1","address":"127.0.0.1","port":3104}`.
The server validates the source, commits the exact NAT mapping and acknowledges
the same ID, address and port with `event:"game.endpoint.bound"`. Unknown fields,
invalid sources, an unnegotiated event or any repeat close the pipe with the
fixed `game-endpoint` category. No packet is used to learn a source.

Discovery is silent before the commit and stays silent for every other source
afterwards. The provider withholds room, advertisement and peer activity until
the acknowledgement, and the server also refuses outbound peer operations while
pending. Identity and platform friend requests remain available. The canonical
endpoint and CommonAddr contract never change during the bind.

## Companion lifecycle

The version-1 protocol is specified in orbiSteamworks'
[companion protocol](https://github.com/pierluigizagaria/orbisteamworks/blob/main/docs/COMPANION_PROTOCOL.md).
Frames are UTF-8 NDJSON of at most 98,304 bytes, and request, event and output
queues are bounded. Operations have a 30-second bound. Logs use stderr; stdout
carries protocol only. The `ready` event follows storage and listener startup,
and stdin EOF starts graceful shutdown.

On shutdown the server logs one fixed `reason` category that distinguishes EOF,
I/O, frame or schema rejection, queue overflow and explicit or context
cancellation; the first cause is recorded before shutdown starts. No frame
contents, identity, credential, peer-provided error string or OS error is
included. On Windows the provider owns this process through a Job Object.

Local mode requires log redaction and also withholds 64-bit identity, session and
connection identifiers; raw captures cannot be enabled. The host supplies
`CTR_COMPANION=stdio`, and the provider sets its own title-independent
`OSW_COMPANION_PROTOCOL=1`.

A minimal local environment, as a provider would supply it (paths are private
and chosen by the integration):

```dotenv
CTR_ADMISSION=local
CTR_COMPANION=stdio
CTR_MATCHMAKING=steam
CTR_AUTH_HTTP=1
BIND_ADDR=127.0.0.10
NEXTENDO_HOST=127.0.0.10
AUTH_PORT=8460
LOBBY_PORTS=3074
NAT_PORT=3074
DASH_PORT=8093
DATA_DIR=data
```

The game's bind address must differ from the server's (for example
`127.0.0.11:3074` versus `127.0.0.10:3074`). No firewall, hosts-file or network
settings are changed, and all publisher and economy functions remain local.

## Room engine and peer boundary

`CTR_MATCHMAKING=steam` selects the provider's room directory. The server's
`Matchmaker` and merge implementation run on the machine of the directory owner.
Only service 145 routing changes: a remote peer has no handler for economy,
statistics, leaderboards, news, storage or other publisher services. The room
request allow-list is tasks 2, 8, 10, 13, 15 and 18 (player information, cancel,
leave, documents, sync and search). Registration and bootstrap replies stay
local, and unknown room operations fail closed. Task data is bounded to 16 KiB
and the complete room envelope to 32 KiB, with 120 messages per second per
authenticated member, 32 outbound pending requests and 64 pending
acknowledgements.

Engine mutations and room invalidation are serialized. Closing a room cancels
pending member requests immediately, discards late activations and rejects
older connection generations. A failed membership snapshot leaves the directory.
An event observed during a membership query invalidates that snapshot, and
selection refreshes it instead of overwriting the event; three unstable snapshots
fail closed and leave the directory. This is a local revision guard, not a remote
revision or wall-clock ordering.

The version-1 peer envelope has a kind (`request`, `reply`, `ack`, `push`),
decimal-string sender PID and connection ID, request ID, task or event, and
base64 typed arguments. The provider authenticates sender identity and
membership; the server also requires sender == claimed PID, current room
membership, owner-only replies and pushes, and increasing request IDs per
connection. An acknowledgement after the member's local game reply preserves the
engine's reply-before-activation ordering. Each acknowledgement has its own
bound.

Directory metadata contains only a SHA-256 fingerprint of the search filter
(`playlist`), `type=ctr-async-1` and the available slots. Search results may omit
members before joining, matching Steam visibility; joined records must contain a
validated owner and the local member. Player addresses and DTLS material travel
only as member room messages. Party searches may claim only current directory
members; committed platform-party followers are joined by the provider and
delivered as `room.selected`. Owner departure invalidates the room, and there is
no engine migration. Rooms in separate directories are not merged, so
simultaneous room creation can form separate rooms until players search again.

Owners without an endpoint mode use a document adapter that rewrites all eleven
endpoints to local proxies. The original game rejects the resulting CommonAddr
identity, so this mode does not work with the original game; use an endpoint
mode. Endpoint modes validate the authenticated public endpoint and
preserve the complete received host document, including every CommonAddr byte,
original host PID and DTLS data.

## Friends and local privacy

Service 12 name lookups use the provider's local cache of in-game Steam friends
and never ask another server for identity or progression. The game's friend page
reads 138/14 `Delta` records before party membership exists, so party-only
messaging cannot serve it. The server therefore publishes its live 138 record
through the provider's private friend advertisement channel. Only in-game,
namespace-matching Steam friends may read it, and no remote writes are exposed.
The record is bounded to 4096 bytes and checked for owner and context; in an
endpoint mode its common address is validated against an authorized canonical
peer endpoint and preserved. Nothing is placed in public lobby data. Reserved,
orphaned, stale-connection and deleted records are excluded. Without a published
record the query returns an empty 138/14 result.

Each local database belongs to one installation. Remote users are never stored
in local player names, statistics or economy. Cross-player reads return only
local data, and missing remote state stays absent. No generic publisher handler
is reachable from peer room messages.

## Diagnostics

Local authentication refusals use the fixed categories `unsupported-task`,
`malformed-envelope` or `owner-capability-mismatch`, and never log credentials
or request bodies. Local admission accepts only the PS4 task-46 envelope.

`CTR_P2P_TIMING=1` adds compact timing records when `CTR_MATCHMAKING=steam`. It
is off by default and does not change protocol behavior or timeouts. Records
contain timestamps, a process-local sequence, stage, operation, code, phase,
duration and outcome, but no arguments, results, addresses or identity fields.
Each record is written synchronously to the private stderr log, so tracing adds
overhead. Sequence numbers do not correlate different processes or prove that a
game processed a delivered message.
