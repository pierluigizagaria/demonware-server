# Implementation and provenance

Demonware Server is an independent CTR:NF server derived from
[NextendoNetwork/nextendo-demonware](https://github.com/NextendoNetwork/nextendo-demonware)
at `c3400c312a739cf7598204a981156959910ed874`. That is also the last reviewed
upstream revision. Useful CTR changes are reviewed and ported individually;
this project does not track upstream through automatic merges.

Original attribution and the [PolyForm Shield license](LICENSE.md) remain
unchanged. See [Credits](CREDITS.md) for authors and dependency provenance.

## Design

One Go module produces one executable. `cmd/ctrnf-server` loads configuration,
composes services and owns startup and shutdown. Components receive their
explicit dependencies; there are no workspace-specific build dependencies.

- `internal/demonware` owns wire framing, cryptography and typed buffers. It
  contains no game rules, listeners, storage or platform admission policy.
- `internal/lobby` owns lobby transport and dispatch. The entrypoint injects
  the CTR service table; the lobby does not import CTR rules.
- `internal/ctr` owns title-specific matchmaking, economy and progression.
- `internal/store` implements the consumer-owned persistence interfaces.
- Platform providers own admission. Steam calls remain in a separate helper
  or companion provider, outside this server.
- `internal/diablo` retains inactive compatibility code and fixtures; no admitted
  title or production entrypoint reaches its service table.

The [protocol contract](docs/DEMONWARE_PROTOCOL.md) defines wire behavior and
responsibility boundaries. The [local companion contract](docs/P2P_LOCAL.md)
keeps Steam identity, endpoint allocation and opaque transport separate from
CTR room messages and application data. Neither interface requires another
component's source tree.

## Persistence and lifecycle

SQLite stores progression and leaderboards in one instance-owned database.
Central instances bind their database to admission trust and economy mode;
local P2P uses account-keyed rows shared across economy modes. See
[Configuration](docs/CONFIGURATION.md#economy-and-storage).

Shutdown stops admission, drains owned work, closes listeners and storage, and
releases the instance lock. Established lobby sessions have no idle or
session-length limit. Client-reported race outcomes remain client assertions; the server's duplicate
reward checks do not provide authoritative race simulation.

## Maintaining the fork

Record the reviewed upstream revision when porting changes. Preserve attribution,
fixed wire vectors and documented defaults. Keep application policy out of wire
code, and pass persistence through the interfaces owned by its consumer.
Document configuration in `example.env`; do not introduce hidden shared state
or a sibling-project build requirement.
