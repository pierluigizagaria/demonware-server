# PS4 fork

Base: `c3400c312a739cf7598204a981156959910ed874` from
[NextendoNetwork/nextendo-demonware](https://github.com/NextendoNetwork/nextendo-demonware).
License unchanged: PolyForm Shield.

This fork serves only Crash Team Racing Nitro-Fueled on PS4 (CUSA13795,
Demonware title 5760), whose players sign in through shadNet. The PS4 game
speaks the same Demonware protocol as the Switch version (handshake, 128-byte
ticket, the service 145/138/12 tasks), so it runs through the original
dispatcher, matchmaking engine and CTR services. The Nintendo sign-in, account
gates, presence reporting and console friend lookups were removed. The Diablo
III code is kept unchanged but is unreachable: only CTR PS4 tickets are issued.

## Design

| Piece | Where | Behavior |
| --- | --- | --- |
| PS4 identity | `platform_psn.go`, `internal/platform/shadnet` | Parses the task-46/title-5760 envelope and verifies the shadNet NPAuth ID token (RS256, JWKS fetched from a configured URL and cached for 5 minutes, renewed in the background during the last minute, issuer/audience/title/scopes, 60 s clock leeway) |
| Ticket admission | `tickets.go`, `handshake.go`, `ctr_umbrella.go` | Lobby and Umbrella accept only tickets issued here in the last 120 s and bound to their identity; a missing response signer fails closed. Tickets live in memory by session tag (the auth handler, lobby and Umbrella share the process; a restart drops every lobby connection and clients authenticate again) and are forgotten one minute after their window. No session files and no all-zero handshake key |
| PS4 tasks | `platform_tasks.go`, `services.go` | 38/6 identity report; raw bandwidth completion; 12/10 getUserIDs (online players, then the name each player was last issued a ticket under, from `player_names` in `ctr.sqlite`; never used for admission). Friend IDs are shadNet account IDs |
| Marketplace | `ctr_marketplace.go`, `ctr_rewards.go` | 80/245,165,154 use `octane_psn` and owner `psn`; purchases accept only original PS4 SDK defaults. 80/246 (Switch only) and unknown tasks fail without ledger writes |
| Unlock-all option | `ctr_economy_policy.go`, `main.go` | Opt-in full catalog ownership is computed at read time. Known-SKU purchases debit nothing and write nothing; unknown SKUs fail. `meta.unlock_all` fixes the mode for the database, independently of admission. Startup logs `unlock-all: on` or `unlock-all: off`. |
| Economy storage | `ctr_sqlite.go`, `ctr_economy.go` | One `<DATA_DIR>/ctr.sqlite` per instance holds accounts, balances, insert-only inventory, race receipts, daily online seconds, challenge progress, Pit Stop refresh state, and an append-only ledger. A narrow backend seam preserves the upstream JSON reference and reward rules; every operation saves state and audit rows in one transaction and writes only changed rows. Race receipts and online seconds older than nine days, beyond the seven-day reward window, are never loaded and are deleted when that account next saves (an inactive account keeps its expired rows). |
| Leaderboards | `ctr_sqlite.go`, `ctr_stats.go`, `main.go` | bdStats shares `ctr.sqlite` with the economy, keeping the original table, batch transactions and ranking. Rank pages and entity reads run in SQL on an index matching the ranking order. The upstream JSON store remains the reference for the contract test. |
| Engine | `ctr_async.go`, `ctr_merge.go` | Every player holds one seat. Party members are the leader's unverified claim, so a search whose claimed followers already hold a seat is refused; the check and the reservation are atomic before the success reply. A leader's new search replaces only its own searches and seat (`detach`, shared with disconnects); a stale connection's disconnect releases no seat the account's newer connection may hold; disbanding an old lobby ends no search. Party followers who leave lose only their seat and the host gets the new membership; explicit merge-eligibility flags; merges cancelled when a source closes or membership changes; handoff before backfill |
| Diagnostics | `diagnostic_redaction.go` | `NEXTENDO_REDACT_LOGS` (default on) keeps only numeric and boolean log arguments (errors show their type) and disables raw dumps |

Established lobby connections have no idle or elapsed-time cutoff; only the
handshake and individual network operations are bounded. Encrypted client
frames must carry the client's own sequence number (1, 2, 3, ...; recovered
from the CTR PS4 1.21 eboot: send counter `conn+0x260`, receive check at
`0x1ac33`); replayed or out-of-order frames are dropped without closing.

Race rewards are deduplicated by the values the payout uses (timestamp, track,
rank band, mode, laps, online time, Golden Wumpa), so fields the reward ignores
cannot repeat a payment. Challenge occurrences are identified the same way by
what their rule reads (name, time, increment, filter, X and Y values). These
values are the client's statement: the server prevents duplicate claims, not
invented races or occurrences. The auth listener bounds reading each request
(headers 15 s, whole request 30 s) and refuses incomplete bodies.

## Running the server

Copy `config/auth.example.json` to a private location and fill in the issuer,
the registered client/title, the exact granted scopes and the trusted
`jwks_url` of the shadNet instance. `example.env` lists every variable.

| Variable | Purpose |
| --- | --- |
| `CTR_AUTH_CONFIG` | Required shadNet trust policy |
| `CTR_AUTH_SIGNING_KEY` | Required RSA key for the CTR response signature (the client must trust its public key) |
| `DATA_DIR` | Persistent state directory (default `data`): `ctr.sqlite` holds the economy, ledger, mode, leaderboards and player names. |
| `CTR_UNLOCK_ALL` | Optional instance-wide unlock-all: unset, `0`, `false` = off; `1`, `true` = on. Any other value fails startup. The database records the mode and refuses a different one. |
| `CTR_START_BALANCE` | Initial Wumpa Coins for new accounts (default `0`); existing balances are preserved. |
| `AUTH_PORT`, `CERT_FILE`, `KEY_FILE` | HTTPS auth listener |
| `LOBBY_PORTS`, `NAT_PORT` | Lobby TCP ports and NAT UDP port |
| `BIND_ADDR` | Optional listener address (for example `127.0.0.1`) |

`CTR_RELAY_PORT_LOW/HIGH` enable the optional UDP relay inherited from upstream
(`CTR_RELAY_PIDS` selects the joiners). Its sockets honor `BIND_ADDR`. A pair is
released when either player has no lobby connection, never because it was
quiet. The relay is not qualified for the PS4 race transport. `openssl` must be
on `PATH` for response signing.

## Two publisher servers

An unlock-all server and a normal economy server are two processes with their own
`DATA_DIR`, listeners and matchmaking pools. Both may trust the
same shadNet issuer. Nothing is shared between them. Unlock-all covers Pit Stop
content only; save-owned unlocks stay with the save. The game caches marketplace
inventory in the save, so give each server its own profile and save.

The database starts fresh: no old wallet JSON, leaderboard database or policy
marker is imported, read, moved or deleted at runtime. The JSON implementations
remain only as references for upstream tests. Schema version 2 is recorded with
`PRAGMA user_version`. Schema creation and the
`meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)` rows `unlock_all=0|1` and
`trust_id` commit together. The trust ID is a hash of the issuer, client ID and
title in `CTR_AUTH_CONFIG`: shadNet account IDs are unique only within it, so a
database refuses to open for another one. A missing, invalid or conflicting mode
or trust ID fails closed on reopen. A version 1 database gains `player_names`
and adopts the configured trust ID in the same transaction that checks its
mode; other versions are refused.

Economy and leaderboard stores share one connection with WAL, `synchronous=FULL`
and a 5-second busy timeout. PIDs use their signed 64-bit bit pattern. Challenge
receipt/distinct maps and refresh receipts use JSON columns. Each economy save
derives the ledger from the persisted before/after account state: one row per
changed currency and one per new item, with timestamp and source. Purchase rows
retain SKU IDs; reward rows retain race receipt hashes or completed challenge
keys (JSON arrays for batches). Starting credits are logged separately. Updates
with no balance or item change add no ledger rows. Inventory and ledger triggers
reject updates and deletions. The instance mutex serializes load/mutate/save;
an exclusive OS lock on `ctr.sqlite.lock` refuses a second process on the same
database. Read-only tools may still open `ctr.sqlite`.

Normal account reads initialize the starting balance atomically. Unlock-all
inventory and balance reads, including purchase balance pushes, write nothing;
reward operations can still create accounts and record earned currency.

## Dependencies

SQLite uses one external module,
`modernc.org/sqlite` v1.59.0 (BSD-3-Clause, pure Go, no cgo), pinned in `go.sum`,
and raises the `go` directive to 1.25. The other modules compiled into the
server are `modernc.org/libc`, `mathutil` and `memory`, `golang.org/x/sys` and
`github.com/remyoudompheng/bigfft` (BSD-style), and `github.com/dustin/go-humanize`,
`github.com/mattn/go-isatty` and `github.com/ncruces/go-strftime` (MIT). Further
modules in `go.sum` are used only by the driver's own tooling.

## Validation

```text
go test -count=1 ./...
go vet ./...
go test -tags=integration -run TestShadNetPublisherInterop .   # needs SHADNET_INTEROP_FIXTURE
```

The integration test consumes tokens from a running native shadNet issuer and
checks verification, the signed response, ticket issuance and lobby admission.

## Following upstream

Fixes from the original repository are merged with `git merge`, so the fork
keeps the upstream layout:

- New behavior lives in files the fork owns: `platform_psn.go`,
  `platform_tasks.go`, `tickets.go`, `ctr_sqlite.go`, `ctr_economy_policy.go`,
  `diagnostic_redaction.go`, `instance_lock_*.go`, `internal/` and their tests.
- Nintendo-only upstream files are deleted: `identity.go`, `gates.go`,
  `accountlookup.go`, `presence.go` and their tests. An upstream change to one
  of them is a modify/delete conflict; keep the deletion.
- Shared upstream files keep their functions and signatures; only the Nintendo
  branches and values were removed or replaced (for example `octane_switch`
  and `nintendo` became the PS4 constants in `platform_psn.go`).
- Diablo III files are unchanged, so their upstream fixes apply as they are.
