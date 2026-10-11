# PS4 platform support

Base: `c3400c312a739cf7598204a981156959910ed874` from
[NextendoNetwork/nextendo-demonware](https://github.com/NextendoNetwork/nextendo-demonware).
License unchanged: PolyForm Shield. Nintendo code, behavior and tests are kept.

Crash Team Racing Nitro-Fueled on PS4 (CUSA13795, Demonware title 5760) speaks
the same Demonware protocol as the Switch version: handshake, 128-byte ticket and
the service 145/138/12 tasks. Only platform identity differs. This branch adds a
PS4 identity adapter in front of the existing auth handler and runs PS4 traffic
through the same dispatcher, matchmaking engine and CTR services as Switch.

## Design

| Piece | Where | Behavior |
| --- | --- | --- |
| Platform boundary | `platform.go` | `platformAuthenticator` (identity, auth extra data, policy); one installed at startup |
| Nintendo | `platform_nintendo.go` | Original identity/gates; zero policy keeps original admission |
| PS4 | `platform_psn.go`, `internal/platform/shadnet` | Parses the task-46/title-5760 envelope and verifies the shadNet NPAuth ID token (RS256, JWKS fetched from a configured URL and cached for 5 minutes, renewed in the background during the last minute, issuer/audience/title/scopes, 60 s clock leeway) |
| Ticket admission | `tickets.go`, `handshake.go`, `ctr_umbrella.go` | PS4 policy: lobby and Umbrella accept only tickets issued here in the last 120 s and bound to their stored identity; failed persistence or a missing response signer fails closed. Ticket and identity files are named by the ticket's session tag and read directly; a pruner deletes them once expired, keeping the latest identity per account in `account_<pid>.json`. 12/10 name lookups use an in-memory index |
| PS4 tasks | `platform_tasks.go`, `services.go` | 38/6 identity report; raw bandwidth completion; 12/10 getUserIDs (shared: online players, then identities already issued a ticket) |
| PS4 marketplace | `ctr_marketplace.go`, `ctr_rewards.go`, platform boundary | Shared 80/245,165,154 use `octane_psn` and owner `psn`; purchases accept only original PS4 SDK defaults. 80/246 and unknown PS4 tasks fail without ledger writes. Nintendo keeps `octane_switch`/`nintendo` and its upstream fallback. |
| Unlock-all option | `ctr_economy_policy.go`, `main.go` | Opt-in full catalog ownership is computed at read time. Known-SKU purchases debit nothing and write nothing; unknown SKUs fail. `meta.unlock_all` fixes the mode for the database, independently of platform or admission. Startup logs `unlock-all: on` or `unlock-all: off`. |
| Economy storage | `ctr_sqlite.go`, `ctr_economy.go` | One `ctr.sqlite` per instance holds accounts, balances, insert-only inventory, race receipts, daily online seconds, challenge progress, Pit Stop refresh state, and an append-only ledger. A narrow backend seam preserves the upstream JSON reference and reward rules; every operation saves state and audit rows in one transaction and writes only changed rows. Race receipts and online seconds older than nine days, beyond the seven-day reward window, are neither loaded nor kept. |
| Leaderboards | `ctr_sqlite.go`, `ctr_stats.go`, `main.go` | bdStats shares `ctr.sqlite` with the economy, keeping the original table, batch transactions and ranking. Rank pages and entity reads run in SQL on an index matching the ranking order. The upstream JSON store remains the reference for the contract test. |
| Engine | `ctr_async.go`, `ctr_merge.go` | Party followers who leave lose only their seat and the host gets the new membership; explicit merge-eligibility flags; merges cancelled when a source closes or membership changes; handoff before backfill |
| Diagnostics | `diagnostic_redaction.go` | `NEXTENDO_REDACT_LOGS` (default on) redacts log arguments and disables raw dumps |

Established lobby connections have no idle or elapsed-time cutoff; only the
handshake and individual network operations are bounded.

## Running the PS4 server

PS4 is selected when `CTR_AUTH_CONFIG` is set. Copy `config/auth.example.json`
to a private location and fill in the issuer, the registered client/title, the
exact granted scopes and the trusted `jwks_url` of the shadNet instance.

| Variable | Purpose |
| --- | --- |
| `CTR_AUTH_CONFIG` | shadNet trust policy (selects the PS4 platform) |
| `CTR_AUTH_SIGNING_KEY` | Required RSA key for the CTR response signature (the client must trust its public key) |
| `D3_SESSIONS` | Ticket store; PS4 uses a subdirectory namespaced by issuer, client and title |
| `CTR_WALLETS` | Instance state directory (default `wallets`): `ctr.sqlite` contains the economy, ledger, mode and leaderboards. PS4 uses `<CTR_WALLETS>/<issuer namespace>/ctr.sqlite`; Nintendo uses `<CTR_WALLETS>/ctr.sqlite`. |
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
`CTR_WALLETS`, `D3_SESSIONS`, listeners and matchmaking pools. Both may trust the
same shadNet issuer. Nothing is shared between them. Unlock-all covers Pit Stop
content only; save-owned unlocks stay with the save. The game caches marketplace
inventory in the save, so give each server its own profile and save.

The database starts fresh: no old wallet JSON, leaderboard database or policy
marker is imported, read, moved or deleted at runtime. The JSON implementations
remain only as references for upstream tests. Schema version 1 is recorded with
`PRAGMA user_version`; other versions are refused (no migrations: nothing has been published). Schema creation and the
`meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)` row `unlock_all=0|1` commit
together. A missing, invalid or conflicting mode fails closed on reopen.

Economy and leaderboard stores share one connection with WAL, `synchronous=FULL`
and a 5-second busy timeout. PIDs use their signed 64-bit bit pattern. Challenge
receipt/distinct maps and refresh receipts use JSON columns. Each economy save
derives the ledger from the persisted before/after account state: one row per
changed currency and one per new item, with timestamp and source. Purchase rows
retain SKU IDs; reward rows retain race receipt hashes or completed challenge
keys (JSON arrays for batches). Starting credits are logged separately. Updates
with no balance or item change add no ledger rows. Inventory and ledger triggers
reject updates and deletions. The instance mutex serializes load/mutate/save;
each instance must exclusively own its database.

Normal account reads initialize the starting balance atomically. Unlock-all
inventory and balance reads, including purchase balance pushes, write nothing;
reward operations can still create accounts and record earned currency.

## Dependencies

SQLite uses the fork's existing external module,
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
