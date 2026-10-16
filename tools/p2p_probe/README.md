# Optional publisher integration fixture

Run from the server repository on Windows after building the publisher and an
orbiSteamworks ABI 2 library. Python uses only its standard library. OpenSSL
must be on `PATH`; the default Go executable is `.toolchain/go/bin/go.exe`.

```powershell
python tools/p2p_probe/run.py --library ../orbisteamworks/build/p2p/orbisteamworks.dll --server bin/demonware-server-p2p-v2.exe
python tools/p2p_probe/run.py --library ../orbisteamworks/build/p2p/orbisteamworks.dll --server bin/demonware-server-p2p-v2.exe --party
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

These are implementation fixtures (I), not live Steam, native game menu, actual
race or two-PC acceptance. Party propagation inside CTR and embedded race
addresses still need game evidence.
