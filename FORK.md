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
| PS4 | `platform_psn.go`, `internal/platform/shadnet` | Parses the task-46/title-5760 envelope and verifies the shadNet NPAuth ID token (RS256, JWKS fetched from a configured URL, issuer/audience/title/scopes, 60 s clock leeway) |
| Ticket admission | `tickets.go`, `handshake.go`, `ctr_umbrella.go` | PS4 policy: lobby and Umbrella accept only tickets issued here in the last 120 s and bound to their stored identity; failed persistence or a missing response signer fails closed |
| PS4 tasks | `platform_tasks.go`, `services.go` | 38/6 identity report; raw bandwidth completion; 12/10 getUserIDs (shared: online players, then identities already issued a ticket) |
| PS4 marketplace | `ctr_marketplace.go`, `ctr_rewards.go`, platform boundary | Shared 80/245,165,154 use `octane_psn` and owner `psn`; purchases accept only original PS4 SDK defaults. 80/246 and unknown PS4 tasks fail without ledger writes. Nintendo keeps `octane_switch`/`nintendo` and its upstream fallback. |
| Economy policy | `ctr_economy_policy.go`, `main.go` | `CTR_ECONOMY` fixes the whole instance to `authentic` (default: the original ledger) or `unlock-all` (every catalog item owned, computed at read time and never written; purchases debit nothing). The policy is recorded in the wallet directory, and an instance refuses a directory recorded for the other policy. It is independent of platform and admission. |
| Leaderboards | `ctr_stats_sqlite.go`, `ctr_stats.go`, `main.go` | Every instance stores bdStats rows in `ctr-leaderboards.sqlite` (WAL, one transaction per upload batch) inside its wallet directory. The upstream JSON store stays as the reference implementation; a contract test checks both give identical ranks. An old `ctr-leaderboards-v1.json` is reported and ignored, not imported. |
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
| `CTR_WALLETS` | Economy state and the leaderboard database; PS4 uses the same issuer namespace as the tickets. Ledger files left in the root by earlier builds are reported at startup and never read or moved |
| `CTR_ECONOMY` | `authentic` (default) or `unlock-all`, for the whole instance |
| `AUTH_PORT`, `CERT_FILE`, `KEY_FILE` | HTTPS auth listener |
| `LOBBY_PORTS`, `NAT_PORT` | Lobby TCP ports and NAT UDP port |
| `BIND_ADDR` | Optional listener address (for example `127.0.0.1`) |

`CTR_RELAY_PORT_LOW/HIGH` enable the optional UDP relay inherited from upstream;
its two-minute quiet-pair reaper applies, so leave it disabled when sessions may
be idle. `openssl` must be on `PATH` for response signing.

## Two publisher servers

An unlock-all server and an authentic server are two processes with their own
`CTR_WALLETS`, `D3_SESSIONS`, listeners and matchmaking pools. Both may trust the
same shadNet issuer. Nothing is shared between them. Unlock-all covers Pit Stop
content only; save-owned unlocks stay with the save. The game caches marketplace
inventory in the save, so give each server its own profile and save.

## Dependencies

The SQLite leaderboard adds the fork's first external module,
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
