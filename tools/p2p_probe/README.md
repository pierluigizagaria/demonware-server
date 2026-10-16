# Optional publisher integration fixture

Run from the server repository on Windows after building the publisher and an
orbiSteamworks ABI 2 library. Python uses only its standard library. OpenSSL
must be on `PATH`; the default Go executable is `.toolchain/go/bin/go.exe`.

```powershell
python tools/p2p_probe/run.py --library ../orbisteamworks/build/p2p-candidate/orbisteamworks.dll --server bin/demonware-server-p2p-v4.exe
python tools/p2p_probe/run.py --library ../orbisteamworks/build/p2p-candidate/orbisteamworks.dll --server bin/demonware-server-p2p-v4.exe --party
```

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

The default run searches separately from both publishers. `--party` creates a
synthetic platform party, sends and accepts a simulated invitation, commits the
second member, and searches once through the leader with both members. It then
requires a follower `room.selected` event and uses the previously returned friend
proxy for UDP. Fields for the other scenario are false because that step was not
run, rather than a failed assertion. Every actual assertion must pass for exit 0.
Both scenarios finally disconnect the second game's lobby socket and require
an owner membership update with only its own seat. In the party scenario the
follower has never sent a room request; this detects an abandoned party seat
even when no remote request object was created on the owner's publisher.
Failures also report fixed event-kind counts from each library context. This
exposed automatic rejoining after an explicit follower departure; the library
now suppresses that room selection until a new selection or participation change.
Use a library build containing that fix (candidate SHA-256
`c914489c651726a65cb19439c8ae5ac9aa6eca1a7794959e22641061932a6a10`
passed the departure regression with publisher v3). The earlier `build/p2p`
DLL is deliberately preserved while a human-operated game may still use it.

These are implementation fixtures (I), not live Steam, native game menu, actual
race or two-PC acceptance. Party propagation inside CTR and embedded race
addresses still need game evidence.
