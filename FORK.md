# Implementation and provenance

Base: `c3400c312a739cf7598204a981156959910ed874` from
[NextendoNetwork/nextendo-demonware](https://github.com/NextendoNetwork/nextendo-demonware).
License unchanged: PolyForm Shield.

Architecture decision (2026-10-14): this is now a detached CTR:NF fork;
useful upstream CTR fixes are reviewed and ported by hand. The last reviewed
upstream commit is `c3400c312a739cf7598204a981156959910ed874`. The binding
implementation plan is [ARCHITECTURE_REFACTOR.md](docs/ARCHITECTURE_REFACTOR.md).
The architecture refactor was completed at `120c965` on
`refactor/ctrnf-architecture`. A subsequent user decision on 2026-10-14 adopts
the independent repository [pierluigizagaria/demonware-server](https://github.com/pierluigizagaria/demonware-server)
and its `main` branch. This publication decision does not establish in-game
acceptance of the refactored build. The previous `feat/ps4-platform` checkpoint
remains at `33dc1c6`. See [repository transition](docs/REPOSITORY_TRANSITION.md)
and [credits](CREDITS.md).

### Accepted protocol extraction, 2026-10-14

The user accepted `33dc1c6` in game with the normal shadNet A/B pair: shared
lobby and races through results. The corresponding server-log segment shows
matched searches, accepted lobby synchronization, leaderboard uploads and
persisted rewards, with no MAC errors. This acceptance does not cover the
guest C/D instance (still on the previous build), LAN/WAN or the subsequent
architecture refactor. Profile B crashed once on `Game:Main` with `0xc0000005`
about five seconds after the first match notification; it did not recur after
restart. The cause remains open and unattributed.

This fork admits only CTR:NF PS4 title 5760 through the configured shadNet or
explicit guest provider. The shared protocol remains in `internal/demonware`;
one CTR service table is wired per instance. Diablo III handlers and generators
are retained in `internal/diablo` for compatibility tests and cannot be reached
by an admitted client. Nintendo sign-in, account gates, presence reporting and
console friend lookups remain removed.

## Design

| Piece | Where | Behavior |
| --- | --- | --- |
| Demonware wire protocol | `internal/demonware` | Shared framing, handshake key derivation, typed buffers and task/reply envelopes. Standard-library dependencies only; no game state, identity policy or listeners. [Contracts and evidence](docs/DEMONWARE_PROTOCOL.md). |
| PS4 identity | `internal/platform/ps4/platform_psn.go`, `internal/platform/shadnet` | Parses the task-46/title-5760 envelope and verifies the shadNet NPAuth ID token (RS256, JWKS fetched from a configured URL and cached for 5 minutes, renewed in the background during the last minute, issuer/audience/title/scopes, 60 s clock leeway) |
| Anonymous admission | `internal/platform/guest/platform_guest.go` | Explicit injected provider, `CTR_ADMISSION=guest`, restricted to unlock-all instances. Reads the unchanged PS4 envelope, assigns a random positive 63-bit publisher ID and a matching nickname of at most 15 characters. No issuer lookup, account, client identity claim or network-address binding. Never a fallback after failed verification. |
| Ticket admission | `internal/auth/tickets.go`, `internal/lobby/handshake.go`, `internal/ctr/umbrella.go` | Lobby and Umbrella accept only tickets issued here in the last 120 s and bound to their identity; a missing response signer fails closed. Tickets live in memory by session tag (the auth handler, lobby and Umbrella share the process; a restart drops every lobby connection and clients authenticate again) and are forgotten one minute after their window. No session files and no all-zero handshake key |
| PS4 tasks | `internal/ctr/platform_tasks.go`, `internal/lobby/services.go` | 38/6 identity report; inherited router compatibility for the exact 18-byte predicate (general protocol attribution unproven); 12/10 getUserIDs (online players, then the name each player was last issued a ticket under, from `player_names` in `ctrnf.db`; never used for admission). Friend IDs are shadNet account IDs |
| Marketplace | `internal/ctr/marketplace.go`, `internal/ctr/rewards.go` | 80/245,165,154 use `octane_psn` and owner `psn`; purchases accept only original PS4 SDK defaults. 80/246 (Switch only) and unknown tasks fail without ledger writes |
| Unlock-all option | `internal/ctr/economy_policy.go`, `cmd/ctrnf-server/main.go` | Opt-in full catalog ownership is computed at read time. Known-SKU purchases debit nothing and write nothing; unknown SKUs fail. `meta.unlock_all` fixes the mode for the database, independently of admission. Startup logs the configured boolean mode. |
| Economy storage | `internal/store/sqlite.go`, `internal/ctr/economy.go` | One `<DATA_DIR>/ctrnf.db` per instance holds accounts, balances, insert-only inventory, race receipts, daily online seconds, challenge progress, Pit Stop refresh state, and an append-only ledger. A narrow backend seam preserves the upstream JSON reference and reward rules; every operation saves state and audit rows in one transaction and writes only changed rows. Race receipts and online seconds older than nine days, beyond the seven-day reward window, are never loaded and are deleted when that account next saves (an inactive account keeps its expired rows). |
| Leaderboards | `internal/store/sqlite.go`, `internal/ctr/stats.go`, `cmd/ctrnf-server/main.go` | bdStats shares `ctrnf.db` with the economy, keeping the original table, batch transactions and ranking. Rank pages and entity reads run in SQL on an index matching the ranking order. The upstream JSON store remains the reference for the contract test. |
| Engine | `internal/ctr/async.go`, `internal/ctr/merge.go` | Every player holds one seat. Party members are the leader's unverified claim, so a search whose claimed followers already hold a seat is refused; the check and the reservation are atomic before the success reply. A leader's new search replaces only its own searches and seat (`detach`, shared with disconnects); a stale connection's disconnect releases no seat the account's newer connection may hold; disbanding an old lobby ends no search. Party followers who leave lose only their seat and the host gets the new membership; explicit merge-eligibility flags; merges cancelled when a source closes or membership changes; handoff before backfill |
| Main-menu news | `internal/ctr/news.go`, `internal/config/config.go` | 104/5 returns per-instance text for location 1 with exact-language overrides; 104/4 acknowledges views without persistence. No identity, economy or statistics changes. |
| Diagnostics | `internal/diagnostics/diagnostic_redaction.go` | `NEXTENDO_REDACT_LOGS` (default on) keeps only numeric and boolean log arguments (errors show their type) and disables raw dumps |

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

Settings are environment variables, as in the other Nextendo servers, and
[`example.env`](example.env) documents every one. Give each instance its own
copy (for example `normal.env`, `unlock-all.env`; git ignores `*.env` except
the example) and start `demonware-server -env normal.env`, or let the deployment supply
the file (systemd `EnvironmentFile=`, `docker --env-file`). `-env` reads the
file as `docker --env-file` does (values kept exactly, no quotes) and lets
variables already in the process environment take precedence. `internal/config/config.go`
reads and validates everything once at startup, shadNet trust included and
before any key is fetched: an invalid value, a missing required one, or an
unknown `CTR_`, `D3_`, `DASH_`, `NEXTENDO_` or `SHADNET_` variable stops the
server with every problem listed. Switches accept `0`/`false` and `1`/`true`.
Keys and certificates stay in their own files, named by path.

| Variable | Purpose |
| --- | --- |
| `CTR_ADMISSION` | `shadnet` (default) or `guest`. Guest requires `CTR_UNLOCK_ALL=1` and empty `SHADNET_*` values. Selection is passed to the auth handler; no mutable global admission switch. |
| `SHADNET_ISSUER`, `SHADNET_CLIENT_ID`, `SHADNET_TITLE_ID`, `SHADNET_SCOPES`, `SHADNET_JWKS_URL` | Required for shadNet admission: issuer, registered client and title, exact granted scopes (space-separated) and the trusted public key set |
| `CTR_AUTH_SIGNING_KEY` | Required RSA-2048 key (exponent 65537) for the CTR response signature; the PS4 game accepts it only with the CTR:NF Server Signature Patch (see [Client side](#client-side)) |
| `DATA_DIR` | Persistent state directory (default `data`): `ctrnf.db` holds the economy, ledger, mode, leaderboards and player names. |
| `CTR_UNLOCK_ALL` | Optional instance-wide unlock-all: unset, `0`, `false` = off; `1`, `true` = on. The database records the mode and refuses a different one. |
| `CTR_START_BALANCE` | Initial Wumpa Coins for new accounts (default `0`); existing balances are preserved. |
| `CTR_NEWS`, `CTR_NEWS_<CODE>` | Optional main-menu ticker text and the 28 exact-language overrides listed in `example.env`; empty overrides fall back to `CTR_NEWS`. See [Main-menu news](#main-menu-news). |
| `AUTH_PORT`, `CERT_FILE`, `KEY_FILE` | HTTPS auth listener |
| `LOBBY_PORTS`, `NAT_PORT` | Lobby TCP ports and NAT UDP port |
| `BIND_ADDR` | Optional listener address (for example `127.0.0.1`) |

`CTR_RELAY_PORT_LOW/HIGH` enable the optional UDP relay inherited from upstream
(`CTR_RELAY_PIDS` selects the joiners). Its sockets honor `BIND_ADDR`. A pair is
released when either player has no lobby connection, never because it was
quiet. The relay is not qualified for the PS4 race transport. `openssl` must be
on `PATH` for response signing.

## Main-menu news

The simplest setup is one default message and only the translations the
operator wants to maintain. For example, put these lines in the instance's
env file:

```dotenv
CTR_NEWS=Welcome to the community server!   ***
CTR_NEWS_IT=Benvenuti sul server della community!   ***
```

An Italian request receives the Italian text; English and every other language
receive the default. `CTR_NEWS_EN` is unnecessary when the default is already
English. Add, for example, `CTR_NEWS_FR` only when a French translation is
available; the 28 supported overrides are a reference, not a required checklist.
The server does not translate text automatically. The client supplies the
language code: in the recorded lab run, profile A's English console setting
selected `en`, independently of the operator's language or account.

Service 104 (`bdMarketingComms`) serves the PS4 main-menu ticker through
104/5 and accepts viewed reports through 104/4. `CTR_NEWS` supplies the default
text. The 28 optional `CTR_NEWS_<CODE>` settings in `example.env` override
the exact game language (uppercase the code and replace `-` with `_` for
the variable name). For example, `CTR_NEWS_IT=Benvenuti!` serves `it` and
`CTR_NEWS_PT_BR=Bem-vindos!` serves `pt-BR`. An unset or empty override falls
back to `CTR_NEWS`, with no English or base-language fallback. An empty default
and no matching override produce no message. Values are read once at startup;
restart the instance to change them. The normal and unlock-all processes may
use different text, independently of their economy mode or players.

Updated or removed text takes effect on the game's next news download, not
immediately in an active ticker. The original client checks for news when the
news widget attaches; its detach path closes the message and clears its cache.
While active it keeps the current text, and a cached inactive message is reused.
No server command to recall an already displayed message has been established.
An empty reply supplies no new message; it does not erase the client's cache.
To withdraw all translations, clear the default and every override, then restart
the instance with players disconnected. Which menu transitions recreate the
widget has not been verified in game.

End each translation with a visible separator such as `   ***` because the
game scrolls consecutive copies without inserting a gap. Optional spaces after
the separator are preserved by the env-file reader and message encoder. Do not
quote values in the env file. The observed menu font displays the message in
uppercase; the operator can still write normal sentence case.

Text must be valid UTF-8, fit in 2047 bytes and contain no control characters
(including NUL, newline, carriage return and tab) or Unicode line/paragraph
separators. The JSON encoded by `encoding/json` must be shorter than 4096 bytes;
escaping can reach this limit before the text limit. Invalid settings and
unlisted override names stop startup with the other configuration errors.

The wire contract was recovered statically from the original CUSA13795 01.21
executable (C): fetch builder `0x90e90`, location/message readers `0x90170`
and `0x91700`, content consumer `0xcda540`, viewed builder `0x91270`.
The instance text, exact-language fallback, SHA-256-derived nonzero message ID,
eight-entry request bound and stateless viewed acknowledgement are replacement
policy (I), not recovered Activision behavior. No third-party service code was
copied. Only location 1 receives one message when capacity allows; all other
locations receive zero. Replies echo the location and request language, encode
only `content_long` in the content blob and send `{}` metadata. Requests need
an admitted PS4 player, bounded typed fields and the exact terminator. Viewed
reports log only counts and message IDs, without changing identity, economy,
statistics or database state.

Use clearly unofficial operator text. This is text only: URLs cannot be opened
from the ticker. Go tests cover the wire contract and configuration. The local
2026-10-12 check on the installed `367d03d` build confirmed the default text in
the main menu, uppercase display and continuous repetition. A subsequent user
screenshot confirmed the tested Latin accents (`à è é ì ò ù`, their uppercase
forms, and `ñ Ñ ç Ç`) in the menu's uppercase rendering. Language overrides and
long-text rendering remain unchecked in game. No 104/4 viewed report was
observed during the first run, including game exit; delivery and
repetition do not depend on receiving it. It must not be treated as a reliable
read receipt or used to count views.

## Client side

This repository contains only the server. With shadNet admission a PS4 player also needs:

- shadNet with the NPAuth ID-token issuer, proposed in
  [shadps4-emu/shadNet#44](https://github.com/shadps4-emu/shadNet/pull/44)
  (not merged yet);
- shadPS4 with NPAuth ID tokens through shadNet, the draft
  [shadps4-emu/shadPS4#5350](https://github.com/shadps4-emu/shadPS4/pull/5350)
  that depends on it;
- the [CTR:NF Server Signature Patch](https://github.com/pierluigizagaria/ctrnf-server-signature-patch)
  for CUSA13795 01.21, so the game accepts replies signed with
  `CTR_AUTH_SIGNING_KEY`;
- the game's Demonware auth, Umbrella and lobby hosts routed to this server,
  whose TLS certificate the client must accept. Client routing and
  certificates are not part of this repository.

### Anonymous unlock-all candidate

For an anonymous instance, set `CTR_ADMISSION=guest`, `CTR_UNLOCK_ALL=1`, omit
the five shadNet trust values, and choose a separate `DATA_DIR`. TLS and the
response signing key remain required. Merely setting unlock-all never disables
account verification. Normal progression instances cannot select guest admission.

The guest provider accepts the original nested PS4 request, including a placeholder
or empty `id_token`; this is explicitly anonymous admission, not successful token
verification. It ignores supplied account IDs and names. Each authentication
gets a new cryptographically random ID and the nickname `G-` followed by that ID
in base 36. Both ticket ID fields, lobby identity, name lookup and Umbrella refer
to the same assigned guest. Fresh-ticket checks, response signing and encrypted
lobby messages still apply. Established sessions have no added timeout.

No account can reclaim the identity after another authentication, so progression
is not associated with a returning person. Guest statistics and names may still
be recorded under their ephemeral IDs in the guest instance's SQLite database;
the mode does not promise network anonymity or zero retained operational data.
Its fixed `ctrnf:anonymous-guests:v1` namespace cannot open a shadNet-bound database,
and a shadNet instance cannot open the guest database. No data migration occurs.

**Client acceptance is still experimental.** Unmodified CTR stops at platform
checks when shadNet is disabled, before sending this request. A separate local
CUSA13795 01.21 game-patch candidate bypasses those game-owned gates, supplies
the unchanged placeholder envelope, and adopts the publisher-assigned ID. An
offline CPU experiment reaches HTTP upload with no NP service calls. On
2026-10-14 the user confirmed a complete coherent two-client race through results
with the 28-edit candidate. The subsequent 27-edit candidate restored local names
and passed a shared-lobby check with both peer names visible. Stock-emulator
qualification remains pending. The server unit/integration fixtures cover
anonymous ticket issuance, lobby handshake, name lookup and Umbrella; these
tests alone do not establish client acceptance. The protocol extraction below
was subsequently accepted with shadNet A/B as recorded above; the guest C/D
instance remains on the previous build.
Platform friends and invitations are not promised for anonymous identities.

## Two publisher servers

An unlock-all server and a normal economy server are two processes with their own
`DATA_DIR`, listeners and matchmaking pools. Both may trust the same shadNet issuer,
or the unlock-all instance can explicitly choose guest admission. Nothing is shared
between them. Unlock-all covers Pit Stop
content only; save-owned unlocks stay with the save. The game caches marketplace
inventory in the save, so give each server its own profile and save.

The database starts fresh: no old wallet JSON, leaderboard database or policy
marker is imported, read, moved or deleted at runtime. The JSON implementations
remain only as references for upstream tests. The initial schema is version 1,
recorded with `PRAGMA user_version`. Schema creation and the
`meta(key TEXT PRIMARY KEY, value TEXT NOT NULL)` rows `unlock_all=0|1` and
`trust_id` commit together. The trust ID is a hash of the issuer, client ID and
title in `SHADNET_*`: shadNet account IDs are unique only within it, so a
database refuses to open for another one. A missing, invalid or conflicting mode
or trust ID fails closed on reopen. Player names and the trust binding are part
of the initial schema. Other versions are refused; there is no migration from
the discarded pre-release lab schemas. The architecture refactor preserves the
accepted version-1 schema, SQL and transaction boundaries; existing version-1
databases reopen without conversion or reinitialization.

Economy and leaderboard stores share one connection with WAL, `synchronous=FULL`
and a 5-second busy timeout. PIDs use their signed 64-bit bit pattern. Challenge
receipt/distinct maps and refresh receipts use JSON columns. Each economy save
derives the ledger from the persisted before/after account state: one row per
changed currency and one per new item, with timestamp and source. Purchase rows
retain SKU IDs; reward rows retain race receipt hashes or completed challenge
keys (JSON arrays for batches). Starting credits are logged separately. Updates
with no balance or item change add no ledger rows. Inventory and ledger triggers
reject updates and deletions. The instance mutex serializes load/mutate/save;
an exclusive OS lock on `ctrnf.db.lock` refuses a second process on the same
database. Read-only tools may still open `ctrnf.db`.

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
go build -o .cache/demonware-server ./cmd/ctrnf-server
go test -tags=integration -run TestShadNetPublisherInterop ./cmd/ctrnf-server   # needs SHADNET_INTEROP_FIXTURE
```

The opt-in integration test consumes tokens from a running native shadNet issuer
and checks verification, the signed response, ticket issuance and lobby admission.
It was not run for this offline architecture task. Full synthetic verification,
package moves and remaining risks are recorded in the
[final report](docs/ARCHITECTURE_REFACTOR_REPORT.md).

## Lifecycle and persistence

SIGINT/SIGTERM follows the same owned cleanup path as a partial startup error.
Admission closes for lobby, auth and dashboard, including HTTP keep-alive requests.
Idle and partial lobby reads are interrupted. A task already admitted finishes
its reply attempt and `AfterReply`, including the existing callback-specific
send-error behavior. Listeners register connections before launching workers.
Registry reaping, NAT, relay sockets and JWKS refresh have explicit stop/join
owners; PROXY header and handoff workers cannot block shutdown indefinitely.

All workers and callbacks complete before `DB.Close`, which precedes release of
the OS instance lock. One 30-second overall budget covers every stage, including
database close. Expiry forces process exit without an early unlock under running
workers. No WAL file is manually removed. Undelivered replies and pushes may be
abandoned during operator shutdown; there is no added session/menu-idle cutoff.

SQLite transactions are atomic and durable at commit (WAL, `synchronous=FULL`).
Success replies follow commits, but the client may never receive a reply for a
committed operation. Atomicity is per transaction, not per task: 125/1 retains
separate race-reward, refresh and challenge transactions. No exactly-once claim
is made. Transaction boundaries and SQL remain unchanged.

Logs use component prefixes and preserve default redaction. `D3_*` names remain
accepted. `D3_PUBFILES_CONFIG` and `D3_RIFTDATA` are inactive; `D3_PUBFILES`
retains generic physical publisher storage excluding Diablo outputs/aliases.
`pubfiles` is a validating no-op, and dashboard `/pubfiles/` is disconnected.

## Following upstream

Last reviewed upstream commit: **`c3400c312a739cf7598204a981156959910ed874`**.
No additional upstream commit was fetched or reviewed during this refactor.
Useful CTR fixes are reviewed and ported by hand, with the reviewed source commit
recorded here. Merging upstream is no longer a design constraint.

One module and executable remain. `cmd/ctrnf-server` owns configuration loading,
wiring, startup and shutdown; packages own their state and accept only their
individual dependencies. `internal/httpserver` owns the two existing HTTP
transports and their admission/drain gate; it adds no listener, endpoint or service.
`internal/diagnostics` owns logging and capture settings. The other boundaries
and mechanical moves are listed in the [architecture report](docs/ARCHITECTURE_REFACTOR_REPORT.md).

Keep original attribution and the PolyForm Shield license. Preserve the
[protocol contract](docs/DEMONWARE_PROTOCOL.md) and its fixed vectors when
porting codec changes. CTR schemas, error policy and economy rules belong in CTR;
the lobby never imports CTR, and CTR never imports the SQLite adapter. Connect
consumer-owned persistence interfaces in the entrypoint. Configuration changes
must remain explicit and documented in `example.env`.

Retained Diablo fixtures are compatibility references, not live Diablo support.
New title support, admission changes or database changes require separate
evidence and an explicit decision; do not connect the inactive table implicitly.
