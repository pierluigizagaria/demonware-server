# P2P NAT diagnosis after a failed connection

Read-only investigation, 2026-10-16. No publisher production code, responder,
installed executable or active game was changed for this investigation.

The human-operated paired Lab game displayed an incompatible local-strict /
remote-strict NAT error. This is original-game evidence of a failed connection,
not proof that a NAT compatibility check prevented the first direct packet.
The next controlled test keeps the publisher unchanged and isolates the
library's independently reproduced pre-party reverse-proxy correction.

## Sources and evidence classes

- **C:** owned PS4 CUSA13795 update 01.21 executable, SHA-256
  `e3b7a068b5e7be0f976102aa223a74425e7817c67e26e21416163291750c1b83`.
  The addresses below are original virtual addresses. Selected instructions
  were decoded in memory with Capstone through the workspace's
  `docs/research/tools/inspect_orbis.py`; no game or firmware was executed or
  modified by the inspection.
- **C:** the earlier serializer/capture evidence in workspace
  `docs/PS4_PEER_TRANSPORT.md`, qualified by the current project plan.
- **I:** publisher branch `feat/p2p-local`, production source through `bbc3ce1`
  and test-only checkpoint `1994d3e`; library source and synthetic transport
  fixtures. Fixture results do not establish game, Steam SDR or WAN acceptance.
- **Human observation:** the user's original-game screenshot. Private logs were inspected using only
  request type/length and transport counters; no identity, token, advertisement
  body, HMAC, security material or packet payload is included here.

## What the local discovery responder does

`cmd/ctrnf-server/instance.go` selects `nat.Server.LocalOnly` whenever admission
is local. `internal/nat/local.go` replies only to exact `1e 03 00` and
`14 02 00 00` packets from loopback, with the actual observed source endpoint
and configured bound publisher endpoint. The `LocalOnly` branch in
`internal/nat/nat.go` drops all other packets before the legacy forwarding
handlers. In particular, flags 2/3, type-10 introductions and type-14 periodic
packets are not answered or forwarded. This remains deliberate bounded
implementation behavior; it is not a complete NAT classifier service.

The original classifier establishes the consequence of the missing flag replies:

| Original address | Observed behavior |
| --- | --- |
| `0x1bf440` | Pumps classifier state at object offset `0x1a4`, with NAT result at `0x1a0` |
| `0x1bf4c9` / `0x1bf4d9` | State 1 sends the exact initial flag-0 probe |
| `0x1bf980`, state-1 branch | Saves the reply's observed client endpoint and advertised alternate endpoint, sends flag 3 and enters state 2 |
| `0x1bf5e6` / `0x1bf5f6` | State 2 retries flag 3 to the primary endpoint |
| `0x1bf6e9` | Exhausted state-2 retries advance to state 3 |
| `0x1bf558` / `0x1bf568` | State 3 sends flag 2 to the advertised alternate endpoint |
| `0x1bf6a1` / `0x1bf6b0` | Exhausted state-3 retries store NAT result 3 and completed state 4 |
| `0x1bfb5b`–`0x1bfbaf` | A state-2 reply succeeds as NAT 1 only if its actual source IP matches the advertised alternate IP and its source port differs from that endpoint's port |
| `0x1bfb23`–`0x1bfb30`, `0x1bfc25` | State 3 compares the new observed client endpoint with the first observed endpoint: equal gives NAT 2, different gives NAT 3 |

The comparison helper `0x185120` compares IPv4 addresses; `0x185350` returns
the endpoint port; `0x1850f0` compares both port and IPv4. Thus the flag-3 result
cannot faithfully be obtained by returning an arbitrary success body from the
existing listener. No such responder change is proposed here.

The native host's separate NetCtl NAT-type value is not the Demonware
classifier result. Returning platform NAT type 2 does not establish that this
classifier, its CommonAddr, or the remote address has NAT type 2.

## CommonAddr and the actual consumer

The 67-byte IPv4 CommonAddr contains eleven six-byte endpoints followed by the
NAT byte at offset 66. The original serializer `0x186100`, specifically
`0x1862ff`/`0x186309`, writes that final byte from object offset `0xa8`.
Deserializer `0x1866c0`, specifically `0x186ad1`/`0x186ad9`, reads it back to
the same field. Getter `0x1860f0` returns the field. This is a separate field
from the local/public endpoint bytes.

The publisher currently rewrites all eleven endpoints in received room host
documents and friend advertisements to the library-allocated loopback proxy.
`internal/ctr/p2p_address.go` preserves byte 66. Own host documents return
unchanged. `internal/ctr/p2p_friends.go` obtains an authorized friend endpoint
before returning the rewritten friend record. Failed endpoint acquisition or
invalid loopback endpoints do not produce a usable rewritten record.

Selected original control flow gives these additional constraints:

- `0x186010` constructs a CommonAddr from a direct endpoint and assigns NAT 1
  at `0x18609d`. The game's NAT display getter `0xa9d080` maps results 1, 2 and
  3 to its Open, Moderate and Strict entries, respectively. Those text entries
  are registered around `0x1894232`–`0x1894261`.
- `0x1be570` contains a compatibility predicate: either endpoint being NAT 1,
  or both being NAT 2, returns true. This inspection did **not** establish an
  active caller of that predicate in the observed join. Its existence alone
  must not be reported as the reason the join failed.
- `0x1af7f8`–`0x1af8d0` reads the remote NAT and chooses the initial traversal
  send operation. Outside policy 2, remote NAT 1 and remote NAT 3 both reach
  direct endpoint sender `0x1bce70`; remote NAT 2 with policy 1, or policy 2,
  instead calls
  `0x1bc9d0`, which also implements introduction handling. `0x1bce70` obtains
  the CommonAddr endpoint list and public endpoint and attempts direct sends.
- `0x40b704`–`0x40b75d` selects the NAT-specific error after a connection
  failure: 3/3, 3/2 and 2/3 select three distinct incompatibility reasons.
  With remote NAT 1 it retains the generic connection-failed reason. Merely
  changing byte 66 could therefore change the reported error without fixing
  the failed transport.

Consequently, the strict/strict screenshot does not prove that changing the
NAT byte is necessary. The existing proxy endpoints are already attempted by
the direct path. The preserved byte may explain the final diagnostic while a
different transport failure explains why no connection was established.

## Independently reproduced transport gap

The library review found a missing pre-party reverse-proxy authorization path:
one player reads a friend's advertisement and receives a friend-scoped local
proxy. Before any shared directory room, its datagram has room ID zero. The
advertisement owner had not performed a reciprocal friend lookup and therefore
had no friend-scoped proxy. The original inbound channel-48 check discarded
that first datagram instead of delivering it to the owner's game.

The observed counter asymmetry is consistent with this gap, but counters alone
do not prove a packet's type or payload. Existing two-publisher probes performed
their first UDP exchange after joining a shared directory room, including their
party variant; they did not test this earlier unilateral advertisement path.

The separate library candidate grants reverse-proxy delivery only after an
authenticated, exact-namespace, currently in-game friend has successfully been
sent an existing local advertisement. It retains the peer and room checks,
does not invent platform-party membership, and revokes the advertisement grant
on withdrawal or friend ineligibility. Its focused regression sends and replies
before room creation, without a reciprocal advertisement read. Negative cases
cover missing authorization, withdrawal and the need for a fresh read after
republication. Original-game acceptance of that candidate remains separate.

## Decision and possible later policy

Keep the publisher, local NAT responses and CommonAddr NAT byte unchanged for
the first controlled reverse-proxy retest. Do not enable legacy introductions,
forward to packet-specified destinations, or add guessed flag replies.

An optional future replacement policy (**I**) could describe a successfully
allocated, authorized loopback proxy as NAT 1 because it is a virtual local
endpoint, while preserving the actual local classifier and all central-mode
behavior. That would describe the proxy, not claim the remote physical router
or Steam SDR path is Open. The owned constructor/enum evidence supports that
interpretation, but neither necessity nor a gameplay benefit is established.
It is **not implemented** by this investigation and must not be presented as
an accepted connection fix.
