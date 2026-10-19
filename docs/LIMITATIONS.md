# Current limitations

This page distinguishes implemented compatibility behavior from complete service
support. It does not replace the revision-specific validation records linked from
the [documentation index](README.md).

## Incomplete service behavior

- **Unknown tasks can return empty success.** The inherited default dispatcher
  logs an unhandled task and sends a zero-error reply with no results. Some
  service-specific handlers explicitly reject unknown operations, but the common
  fallback does not. Successful login or the absence of a client error therefore
  does not demonstrate that every requested service is implemented. See
  [the default dispatcher](../internal/lobby/dispatch.go).
- **Some matchmaking bootstrap operations are compatibility replies.**
  Init/start/status/DCQoS tasks return a string containing the player's ID; QoS
  reports and expect-game acknowledgements return empty success. They do not
  implement a full datacenter selection or quality-of-service system. Actual
  search, room documents and synchronization have separate handlers. See
  [CTR dispatch](../internal/ctr/dispatch.go).
- **P2P room ownership does not migrate.** Owner departure or an owner change
  invalidates the room. There is no merge between separate provider directories;
  simultaneous searches can create separate rooms until players search again.
  See [P2P room handling](../internal/ctr/p2p.go) and
  [the room contract](P2P_LOCAL.md#room-engine-and-peer-boundary).
- **Rewards trust client reports.** Race and challenge receipts prevent duplicate
  claims using the reported event fields. They do not prove that a race occurred
  or validate the result against an authoritative game simulation. See
  [race rewards](../internal/ctr/rewards.go) and
  [challenge rules](../internal/ctr/challenge_rules.go).
- **The inherited central UDP relay is not qualified for PS4 races.** It is
  disabled by default and separate from provider-backed local P2P transport.

The local companion protocol permits only a bounded set of room operations
across peers. Its rejection rules are not equivalent to the central dispatcher's
inherited fallback behavior.

## Deliberate scope

- **CTR:NF PS4 only.** Title 5760 is the only admitted title. Retained Diablo
  handlers and fixtures are compatibility references, not executable support.
- **Internal protocol code.** `internal/demonware` is separated from game rules
  for maintenance and reuse within this module. Go's `internal` boundary prevents
  treating it as a supported external SDK.
- **Independent components.** The server uses explicit admission and companion
  contracts. It supplies neither Steam services nor a host/emulator, game assets,
  client routing or the signature patch. Local mode intentionally requires a
  companion parent; central modes remain independently deployable.
- **Local publisher data.** P2P does not synchronize economy or leaderboards
  between installations. Remote peers cannot use room messages to query those
  publisher services.
- **Different Steam admission paths.** Central helper-based admission is fixed
  to AppID 480. Local P2P receives its configurable AppID from the companion owner
  frame. A configurable local AppID does not make the central path configurable.

## Validation boundaries

Ordinary tests verify server contracts with synthetic clients and temporary data.
Optional real-provider tests verify only their documented integration. Human
gameplay reports apply to the exact artifacts and conditions recorded in
[FORK.md](../FORK.md) and [P2P_LOCAL.md](P2P_LOCAL.md#evidence-status).
They do not establish arbitrary-host support, Internet transport, capacity or
public-service readiness. Save-cached inventory, equipped items across economy
changes and host process teardown require client-side validation as well.
