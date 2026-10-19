# Demonware Server

A game publisher server for **Crash Team Racing Nitro-Fueled on PS4**, built
around a reusable Demonware protocol implementation. The supported client is
**CUSA13795 01.21**, Demonware title **5760**, running with shadPS4.

One Go executable handles publisher authentication, encrypted lobby requests,
matchmaking, economy, leaderboards and NAT discovery. Player accounts belong to
an independent shadNet service; race traffic remains separate from publisher
services. The current executable admits CTR:NF only.

This is an independent repository derived from the Nextendo Demonware server.
Original authors, contributions and research references are listed in
[CREDITS.md](CREDITS.md). It is unofficial and is not affiliated with Sony,
Activision, Demonware, Nintendo, Nextendo Network or the shadPS4 project.

## Features

- PS4 publisher login using shadNet ID tokens and a configured public JWKS endpoint.
- Unsigned authentication replies (with the client signature patch), locally issued tickets
  and encrypted lobby sessions.
- Async matchmaking, contextual friend sessions, rich presence and lobby synchronization.
- PS4 marketplace, race rewards, challenges, leaderboards and configurable menu news.
- One SQLite database per instance. Central instances keep separate normal and unlock-all
  economies; a local P2P database is shared by its accounts and both modes.
- NAT discovery/introductions and the existing monitoring endpoints.
- Owned workers and graceful operator shutdown; established lobbies have no idle cutoff.

## Instance modes

| Mode | Admission | Economy |
| --- | --- | --- |
| Normal | Verified shadNet account | Online progression, starting from zero by default |
| Unlock-all | Verified shadNet, or explicitly configured anonymous guests | Pit Stop catalog ownership; separate database and matchmaking pool |

Anonymous admission requires both `CTR_ADMISSION=guest` and `CTR_UNLOCK_ALL=1`.
Each authentication gets a new publisher identity; it is not an account and
cannot recover a previous guest identity. The client needs the corresponding
anonymous game patch. Unlock-all does not control single-player unlocks owned
by the save. Each instance needs separate listeners and a separate `DATA_DIR`.

## Status

**Experimental.** A local two-player shadNet session on baseline `33dc1c6` was
accepted through shared lobby, racing and results on 2026-10-14. Anonymous
two-client racing was accepted on an earlier build. The architecture refactor
and this repository transition have automated coverage but have not yet been
installed or accepted in game.

LAN/WAN, capacity, public hosting and the optional relay are not qualified.
Rewards use client-reported events; this is not authoritative race validation.
Retained Diablo III code is inactive compatibility material, not supported
game admission. The detailed acceptance history and open issues are in
[FORK.md](FORK.md) and the [architecture report](docs/ARCHITECTURE_REFACTOR_REPORT.md).

## Build and test

Requirements: **Go 1.25 or newer**. No C compiler or OpenSSL is required.

```sh
git clone https://github.com/pierluigizagaria/demonware-server.git
cd demonware-server
go build -o bin/demonware-server ./cmd/ctrnf-server
go vet ./...
go test -count=1 ./...
```

On Windows, use `bin/demonware-server.exe` for the output name. The command
directory remains `cmd/ctrnf-server` because CTR:NF is the admitted title;
the module is `github.com/pierluigizagaria/demonware-server`.

For an offline checkout with the local toolchain and dependencies already
present, use `.toolchain/go`, set `GOCACHE` to the absolute `.cache/go-build`
path and set `GOPROXY=off`. Generated binaries and caches are ignored by Git.

The suite preserves exact dispatcher and JSON compatibility references, including
the SQLite JSON columns. See the [JSON inventory](docs/DEMONWARE_PROTOCOL.md#json-compatibility-references)
before changing a serialized field or persisted data contract.

Unit tests live beside their component implementations. `cmd/ctrnf-server`
retains composed admission, dispatch, dashboard, instance and shutdown tests,
including the immutable 648-snapshot dispatcher baseline. Component ownership
and copied observation APIs are documented in the
[state boundary notes](docs/ARCHITECTURE_REFACTOR.md#component-state-and-observation-apis).
A bounded wire-parser fuzz run can be repeated with:

```sh
go test ./internal/demonware -run '^$' -fuzz '^FuzzWireParsers$' -fuzztime=15s
```

## Configuration

Start from [example.env](example.env). Configuration is validated once at
startup; process environment values take precedence over an instance env file.
Private env files, certificates and databases are not distributed.

```sh
./bin/demonware-server -env normal.env
```

Before connecting a client, configure:

1. The admission provider and instance policy: `CTR_ADMISSION`, `CTR_UNLOCK_ALL`
   and a dedicated `DATA_DIR`.
2. For shadNet admission, the trusted `SHADNET_*` issuer, client, title, scopes
   and public JWKS URL. shadNet owns the accounts; no private key or database
   is shared with this server.
3. TLS (`CERT_FILE`, `KEY_FILE`), listener addresses/ports and routing to your own
   server. Authentication replies are unsigned.
4. The required client-side authentication integration and
   [CTR:NF Server Signature Patch](https://github.com/pierluigizagaria/ctrnf-server-signature-patch).
   Server settings alone do not replace the client prerequisites.

See [client requirements](FORK.md#client-side) and
[instance configuration](FORK.md#running-the-server) for details.
Logs are redacted by default. Legacy `D3_*` variable names remain accepted;
their active or inactive roles are documented in `example.env`.

## Architecture and data

`cmd/ctrnf-server` composes independent component owners in one process:

- `internal/demonware`: wire framing, buffers, crypto and reply envelopes.
- `internal/auth` and `internal/platform`: admission, signing and tickets.
- `internal/lobby` and `internal/ctr`: transport/router and CTR service adapters/business rules.
- `internal/store`: SQLite persistence; `internal/presence`: shared identity views and presence.
- `internal/nat`, `internal/relay`, `internal/dashboard`: networking and monitoring.

SIGINT/SIGTERM stops admission and drains admitted work before closing SQLite
and releasing the instance lock. A single 30-second limit covers the complete
drain; expiry terminates the process. Replies and pushes can be lost during
operator shutdown.

`ctrnf.db` uses WAL and `synchronous=FULL`. Atomicity and durability apply to
each committed transaction, not necessarily a whole task. A commit may survive
without its response reaching the client; there is no exactly-once guarantee.
The repository transition changes no schema, protocol or economy rule.

## Documentation

- [Implementation, configuration and provenance](FORK.md)
- [Demonware contracts and evidence](docs/DEMONWARE_PROTOCOL.md)
- [Architecture plan and inventory](docs/ARCHITECTURE_REFACTOR.md)
- [Architecture implementation report](docs/ARCHITECTURE_REFACTOR_REPORT.md)
- [Independent repository transition](docs/REPOSITORY_TRANSITION.md)
- [Credits](CREDITS.md)

## License

This project retains the **[PolyForm Shield License 1.0.0](LICENSE.md)** and
the original required notice. It is source-available software; the repository
transition does not change its licensing terms, including the Noncompete terms.

Required Notice: Copyright 2026 Nextendo Network
