# Optional publisher integration fixture

Run from the server repository on Windows after building the publisher and an
orbiSteamworks ABI 2 library with the optional virtual endpoint C API. The main
fixture uses Python's standard library. OpenSSL
must be on `PATH`; the default Go executable is `.toolchain/go/bin/go.exe`.

```powershell
python tools/p2p_probe/run.py --library ../orbisteamworks/build/virtual-candidate/orbisteamworks.dll --server bin/demonware-server-canonical-candidate.exe
python tools/p2p_probe/run.py --library ../orbisteamworks/build/virtual-candidate/orbisteamworks.dll --server bin/demonware-server-canonical-candidate.exe --party
```

Use current candidates containing `osw_enable_virtual_endpoints`,
`osw_get_local_endpoint`, `osw_route_endpoint` and the publisher's
`virtual.endpoints.v1` capability. Neither project is linked into the other: the optional fixture
loads only the supplied DLL's public C API and launches the supplied executable.

The fixture enables canonical endpoints before each library context starts.
Its synthetic game verifies the exact-source NAT discovery result, publishes a
CommonAddr containing the canonical public endpoint and a distinct local port,
and requires the friend and room records to preserve every byte. A private pipe
bridge calls the generic C API to map outbound canonical endpoints to authorized
physical proxies and inbound proxy sources back to canonical endpoints. UDP is
sent only to the returned physical proxies, so a direct connection to the other
fixture game's bind cannot mask a broken provider route. Both pre-room and room
traffic require the correct canonical source in each direction.

Add `--owned-identity-proof --owned-image PATH --image-inspector PATH --rhash-library PATH` to run the bounded
original-instruction CommonAddr comparison against the **actual synthetic
advertisement returned by the publisher**. It requires the owned hash-pinned
image, an OrbisImage inspector, Python Unicorn and a trusted local
librhash. It verifies byte-exact preservation, distinct IDs for the two
canonical endpoints, acceptance of the correct advertisement and rejection of
the other identity. Only aggregate booleans are emitted; the private report is
under that run's ignored directory. This optional research dependency is outside
ordinary server build/test, and remains distinct from a running game test.
The three paths are mandatory only for that optional proof; the tool makes no
assumption about sibling repositories or the operator's workspace layout.

Both canonical scenarios passed on 2026-10-16, including the optional original
comparison with the actual returned synthetic advertisement. The original gate
accepted the preserved ID, rejected the other canonical ID, and reproduced the
previous all-slot-rewrite rejection. The pre-room and room routes both preserved
canonical source identity in each direction; the party variant also retained
follower selection and disconnect seat removal. Redacted aggregate logs are
`.cache/p2p-probe/canonical-default.log` and `canonical-party.log`. These are
implementation fixtures (I), not a completed original-game connection or race.

The fixture reserves publisher loopbacks `127.0.0.60` and `127.0.0.70` (TCP
8460, 8093, 3074; UDP 3074), and game loopbacks `127.0.0.61:3074` and
`127.0.0.71:3074`. It does not replace installed executables or stop existing
services. Each run creates a unique directory under `.cache/p2p-probe/`; its
private key, database, lab hub, binary and redacted child logs stay ignored.
The library's Job Objects dispose the publisher children when contexts close.

The synthetic clients verify the HTTP response's RSA-PSS signature, authenticate
over the original PS4 envelope, perform encrypted Demonware handshakes/tasks,
look up a friend advertisement and online ID, prove a foreign statistics read
returns no row, create/join the existing async room engine, and exchange opaque
UDP through the actual library proxies. Tokens are passed only in memory and
stdin. The helper emits fixed step labels and aggregate booleans; it never
prints request bodies, keys, tokens, persona strings or raw logs.

Before either directory search, only the second client reads the first client's
advertisement and immediately exchanges UDP in both directions through the
proxy obtained by routing its canonical endpoint. The advertiser performs no reciprocal lookup or endpoint
request. With the default invocation this also precedes any platform party;
`--party` has already created its synthetic party but still has no directory
room at that point. This pre-room assertion prevents shared room membership
from hiding a missing unilateral friend bootstrap path.

The default run searches separately from both publishers. `--party` creates a
synthetic platform party, sends and accepts a simulated invitation, commits the
second member, and searches once through the leader with both members. It then
requires a follower `room.selected` event and routes the previously advertised friend
endpoint for UDP. Fields for the other scenario are false because that step was not
run, rather than a failed assertion. Every actual assertion must pass for exit 0.
Both scenarios finally disconnect the second game's lobby socket and require
an owner membership update with only its own seat. In the party scenario the
follower has never sent a room request; this detects an abandoned party seat
even when no remote request object was created on the owner's publisher.
Failures also report fixed event-kind counts from each library context. This
exposed automatic rejoining after an explicit follower departure; the library
now suppresses that room selection until a new selection or participation change.
Use a library build containing that fix. Both scenarios passed on 2026-10-16
with publisher v4 built from clean commit
`55ebb84a77535a37b636c62d8279bffd0486f77f`, SHA-256
`0dcb7fc98b406b43602d6cc03addcb2ea262ea2451226a92dcd8c05ccb20df91`,
and the final isolated library candidate, SHA-256
`bc172ccc8beddf8f2045d4420407a660ce4484de12a3f456ab037c7663f110e1`.
The earlier `build/p2p` DLL is deliberately preserved while a human-operated
game may still use it.

The unilateral pre-room assertion added later on 2026-10-16 fails with library
`508a135f34052108481c7427a3e8633b7d7b9a48794a071feef41ad2243e3786`
at the fixed step `unilateral pre-room delivery`. Both scenarios pass with
`build/preparty-candidate/orbisteamworks.dll`, SHA-256
`8623d614894d7ac7781e5e2c99ef67986c612211de968b087ff66faebcd8132e`,
and the unchanged `bin/demonware-server-rename-candidate.exe`, SHA-256
`0fc70159dad030a978f7126e25368fec4e911cc8d418980f7268fe290a027369`.
Private logs are `.cache/p2p-probe/preparty-before-fix.log`,
`preparty-after-fix-default.log` and `preparty-after-fix-party.log`.

These are implementation fixtures (I), not live Steam, native game menu, actual
race or two-PC acceptance. Party propagation inside CTR and embedded race
addresses still need game evidence.
