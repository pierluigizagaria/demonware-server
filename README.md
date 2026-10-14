# CTR:NF PS4 publisher server

A detached fork of Nextendo for Crash Team Racing Nitro-Fueled on PS4
(CUSA13795 01.21, Demonware title **5760**), used with shadPS4. One Go module
and executable provide publisher authentication, encrypted lobby tasks,
matchmaking, economy, leaderboards, NAT and the existing dashboard API.
There are no additional services introduced by the architecture refactor.

Normal instances verify ID tokens from an independent shadNet service through
its configured public JWKS endpoint. An explicit anonymous provider is available
only with `CTR_ADMISSION=guest` and `CTR_UNLOCK_ALL=1`. Normal and unlock-all
instances keep separate databases, listeners and matchmaking state. Unlock-all
does not control save-owned single-player unlocks. See [FORK.md](FORK.md) for
admission, client prerequisites, economy policy and the two-instance setup.

This project is unofficial and is not affiliated with Sony, Activision,
Demonware, Nintendo, Nextendo Network or the shadPS4 project.

## Status and evidence

The user accepted protocol baseline `33dc1c6` on 2026-10-14 with shadNet A/B:
shared lobby and races through results, with matching server-log evidence for
matchmaking, lobby sync, leaderboard uploads and persisted rewards, without
MAC errors. Guest C/D remained on the previous build. Profile B crashed once
on `Game:Main` (`0xc0000005`) shortly after a match notification; it did not recur
after restart and the cause remains unattributed.

The architecture work is local on `refactor/ctrnf-architecture`, from
`feat/ps4-platform` at `33dc1c6`. It has **not been installed or accepted in game**.
Review and later acceptance of both A/B (normal) and C/D (guest unlock-all)
remain separate. LAN/WAN, capacity and public hosting are unqualified.

- [Binding plan, inventory and fixture ledger](docs/ARCHITECTURE_REFACTOR.md)
- [Final implementation and validation report](docs/ARCHITECTURE_REFACTOR_REPORT.md)
- [Demonware wire contracts and evidence](docs/DEMONWARE_PROTOCOL.md)

## Build and configuration

From the repository directory, with Go 1.25 or newer and the pinned module
dependencies already available:

```text
go build -o .cache/ctrnf-server ./cmd/ctrnf-server
go vet ./...
go test -count=1 ./...
```

The local Windows verification uses `.toolchain/go`, `GOCACHE=.cache/go-build`
and `GOPROXY=off`; the final executable is kept under
`.cache/architecture-refactor/ctrnf-server.exe`. It is not installed by these
instructions. Existing OpenSSL on `PATH` is required for response signing.

All environment names and their validation remain unchanged; see
[example.env](example.env). Each instance reads its own immutable configuration:
`ctrnf-server -env normal.env`, with process environment taking precedence.
Private env files, keys, credentials, databases and runtime captures are ignored.
The client routing, TLS certificate and signature patch are separate prerequisites
documented in [FORK.md](FORK.md#client-side).

`D3_PUBFILES_CONFIG` and `D3_RIFTDATA` remain accepted but are inactive.
`D3_PUBFILES` still locates generic physical files for service 10/21, excluding
the documented Diablo outputs and aliases. The `pubfiles` command validates
configuration and exits without generating files. `/pubfiles/` is not registered.
Diablo code and tests remain in `internal/diablo`; no admitted title reaches it.

## Architecture and shutdown

`cmd/ctrnf-server` composes `config`, `auth`, platform providers, `lobby`, `ctr`,
`store`, `presence`, `nat`, `relay`, `dashboard`, `httpserver` and `diagnostics`.
`internal/demonware` remains independent of game rules. The lobby consumes a
narrow dispatch contract; CTR owns its adapters and business rules, and SQLite
implements the persistence interfaces consumed by CTR.

SIGINT/SIGTERM closes admission, interrupts idle/partial lobby reads and drains
admitted handlers, replies, callbacks and background workers. Only then does
SQLite close and release its instance lock. One 30-second budget includes
`DB.Close`; expiry forces process exit without unlocking under active workers.
Operator shutdown can abandon undelivered replies and pushes. Established
sessions have no idle or elapsed-time cutoff.

SQLite uses WAL and `synchronous=FULL`: atomicity and durability are per committed
transaction. Success replies follow commits, but a commit may survive a lost
reply. A task can contain several transactions (125/1 rewards, refresh and
challenges); there is no exactly-once delivery promise. Schema version 1, SQL and
transaction boundaries are unchanged, and existing databases open unchanged.
Never delete the WAL manually.

## Attribution and license

Derived from [nx-mod's original server](https://github.com/nx-mod/nextendo-demonware)
and [NextendoNetwork/nextendo-demonware](https://github.com/NextendoNetwork/nextendo-demonware).
The original CTR support and reverse engineering were contributed by
[CollectingW](https://github.com/CollectingW). Original credits and historical
Switch/Diablo evidence remain in [the archived documentation](docs/HISTORICAL_SWITCH_README.md).
Useful upstream CTR fixes are reviewed and ported by hand; the reviewed commit
is recorded in [FORK.md](FORK.md#following-upstream).

The [PolyForm Shield License 1.0.0](LICENSE.md) is unchanged. This is
source-available software; consult the license, including its Noncompete terms,
before any distribution or service decision.
