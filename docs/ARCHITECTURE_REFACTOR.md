# CTR:NF server architecture refactor

Decision: 2026-10-14, by the user, after review of `33dc1c6` (protocol
extraction, accepted in game with the shadNet A/B pair on 2026-10-14). The plan
below was reviewed by Codex and Claude in two read-only rounds before
implementation; this is the agreed version.

## Decisions

- **Full detachment from upstream.** This repository becomes a CTR:NF server
  derived from Nextendo. `git merge` from upstream is no longer a constraint.
  Useful upstream CTR fixes are reviewed and ported by hand; FORK.md records the
  last reviewed upstream commit. Attribution and the PolyForm Shield license stay.
- **Diablo III stays as an inactive package.** Its code and tests move to their
  own internal package. No admitted title (today only CTR PS4 5760) reaches it.
  Its tests remain a compatibility reference for `internal/demonware`.
- **Codex implements every phase, then one final review** by the user and Claude.
  Because there are no intermediate reviews, the stop conditions below are binding.

Protected contracts: one Go module and executable, no new services, processes,
third-party modules or network APIs. Same wire behavior, admission, ticket,
sequence, economy, unlock-all, guest and news policies. Same `ctrnf.db` schema
(version 1), SQL and transaction boundaries, so existing databases open
unchanged. Same environment variable names and acceptance, including `D3_*`
(document the ones that become inactive; rename or remove none). No idle or
elapsed-time cutoffs on established sessions. Log redaction stays on by default.

## Working rules

- Work on a local branch `refactor/ctrnf-architecture` from `feat/ps4-platform`.
  Fast-forward `feat/ps4-platform` only after the user accepts the final review.
  Never push.
- One logical change per commit, with a message body. Mechanical extraction
  commits may change only what compilation requires (package clause, imports,
  visibility); no semantic change in the same commit.
- `gofmt`, `go vet ./...` and `go test -count=1 ./...` pass before every commit.
  A failing test during development is fixed, not committed; it triggers a stop
  only when preserving behavior would require changing a protected contract.
- Do not install binaries in the lab, start game profiles or operate the game.
  Build the final executable under `.cache/` only.
- Review staged paths before each commit; never commit keys, credentials,
  databases, `runtime/`, `.cache/`, `bin/` or env files other than `example.env`.

## Phase 0: records, inventory and safety net

1. Record the 2026-10-14 in-game acceptance of `33dc1c6` (shadNet A/B, shared
   lobby and races through results; server log shows matched searches, accepted
   lobby syncs, leaderboard uploads and persisted rewards, no MAC errors) in
   FORK.md, `docs/DEMONWARE_PROTOCOL.md` and the workspace `docs/` files that
   still list it as pending (`multiplayer_plan.json`, `START_HERE.md`,
   `SHAREABLE_PLAN.md`; outside this repository). Not covered: the guest C/D
   instance (still on the previous build) and LAN/WAN. Open and unattributed:
   profile B crashed once (`Game:Main`, `0xc0000005`) about 5 s after the first
   match notification and did not recur after restart.
2. Inventory as a matrix, stored in this document (pairs only, no identities):
   service/task, evidence source, build/run segment observed, current code
   behavior, covering fixture. Segment lab logs by run/build (for example 104/5
   was a default answer on 2026-10-12 and is handled today). Ambiguous log lines
   stay ambiguous. **Not observed never means unused.**
3. Dispatcher characterization harness, built on the existing in-memory
   `platformWire`/`sharedPS4Lobby` fixtures (`net.Pipe` only for blocking reads,
   disconnects and shutdown). For every handled pair, the default branch and the
   malformed branches it captures all frames, verifies MAC and sequence, and
   compares inner type and decrypted payload (never random ciphertext), pushes,
   send-failure behavior (a connection failing a chosen write) and resulting
   in-memory and persisted state.
4. Determinism: record fixtures and invariants on the unmodified baseline first,
   then add small `now`/entropy seams in the affected components only (no global
   `rand.Reader` replacement), verified against that evidence. Normalize only
   documented fields (for example `getServerTime` within a range); never
   normalize reward timestamps wholesale, since they feed receipts, validity
   windows and multipliers. Auth: `httptest`, synthetic providers, an ephemeral
   key, signature verified before any body normalization.

## Phase 1: composed instance instead of package globals

- An instance owner, composed of per-component structs (stores, tickets,
  presence, matchmaker, relay, diagnostics, runtime resources). Each component
  receives only the dependencies it needs; handlers do not get a pointer to
  everything.
- Validated configuration is an immutable value. Defaults become constants or
  freshly built values: `readConfig` must not read globals that `applyConfig`
  overwrote (`config.go` today).
- Process-wide settings move to their owners: `redactLogs`, `verbose`,
  `dumpRoot`, `runStamp` to instance diagnostics; `migrateEnabled` to lobby
  options; `pubDir`, `riftDir` to their file providers.
- Counters get explicit owners and keep their increment points: `transactions`
  (instance handlers), `serverIDs` (lobby, same prefix and sequence), `ctrIDs`
  (matchmaker, shared by searches and rooms), `connNum` (instance allocator; it
  also names NAT dumps).
- Package level keeps only constants, sentinel errors, regexps and truly
  immutable embedded data; read-only maps and slices stay private and tests
  never mutate them.
- **Acceptance:** two instances in one process, with numerically equal
  identities, are isolated in tickets, names, matchmaking, pushes, configuration
  and economy.

## Phase 2: one CTR service table per instance

- The wiring connects one CTR service table to the instance. The Diablo table
  exists in its package and is not connected. No per-session table selection
  until a second title is admitted.
- Handler contract: `Handle(session, request) Result`. The session exposes the
  connection ID, publisher identity, admitted title, `PID()` with today's
  behavior for a missing identity, redacted logging and push sending; never
  sockets, keys or databases. The request carries parsed arguments and the
  original payload (marketplace and achievements read the structured header
  offset). `Result` holds the reply and `AfterReply(sendErr error)`, called
  exactly once after the send attempt, also when it fails, preserving today's
  differences: 145/18 releases its reservation on send error; marketplace and
  achievements callbacks do not check it.
- The router keeps the 18-byte special case before ordinary parsing, no reply
  for payloads under three bytes, error for unreadable tasks on 80/104 and empty
  success otherwise, and empty success for unknown services.
- Conditions that live in the dispatcher today stay with adapters, not inside
  plain method maps: for example service 4 with no player returns empty success,
  while calling `onCTRStats` directly would return an error. Per-service
  defaults stay (unknown marketplace tasks and 80/246 fail; achievements default
  is a structured reply). Argument validation stays in handlers.
- Remove from the CTR table only behavior the code shows to be Diablo-specific
  (service 21 public matchmaking handlers, Challenge Rift and season file
  generation). Service 138 contextual sessions are shared registry plus CTR
  parsing: separate the shared registry from the CTR and Diablo adapters.
- Define the fate of every Diablo path explicitly: generators, physical files
  served by `publisherFile`, aliases, the `pubfiles` command and the dashboard
  `/pubfiles/` route. If CTR uses `getPublisherFile`, CTR keeps only its own
  files.
- List every fixture that changes and why; these are the only intended changes.

## Phase 3: packages by responsibility

| Package | Contents |
| --- | --- |
| `cmd/ctrnf-server` | flags, config loading, wiring, start and stop |
| `internal/config` | environment parsing and validation |
| `internal/lobby` | listener, handshake, session, sequence policy, router; declares the dispatch contract it consumes |
| `internal/auth` | HTTPS auth, tickets, response signing, PROXY protocol |
| `internal/platform/...` | PS4 envelope, shadNet verifier (exists), guest |
| `internal/ctr` | one cohesive package at first: economy, marketplace, rewards, challenges, matchmaking engine, stats, news, achievements, Umbrella, CTR adapters |
| `internal/store` | `ctrnf.db` adapter implementing interfaces declared by `internal/ctr`; connected by the wiring, never imported by CTR |
| `internal/presence` | rich presence and friends shared across titles |
| `internal/nat`, `internal/relay`, `internal/dashboard` | as named; dashboard and presence get views or callbacks, never internal mutexes of lobby or matchmaker |
| `internal/diablo` | inactive Diablo III handlers, pubfiles, seasons, rifts and their tests |
| `internal/demonware` | unchanged wire protocol |

Seams: `lobby` never imports `ctr`; CTR adapters may import `lobby`. The CTR
engine consumes a small `Peer` interface declared in `ctr` and implemented by
the session: keyed by `ConnID` (not PID), so a stale connection's disconnect
never releases seats held by the account's newer connection; notifications are
still built under the lock and sent after releasing it. No import cycles, no
`util`/`common` packages, interfaces declared by consumers only where two
implementations or a test seam exist. Embedded JSON moves with its package.
Keep the module path unchanged.

## Phase 4: lifecycle and finish

Operator-initiated shutdown on `SIGINT`/`SIGTERM`, with no new SQL contexts:

1. Stop admitting new connections and new requests, HTTP/auth included.
2. Interrupt idle or partial lobby reads; let tasks already inside a handler
   finish their reply and `AfterReply`. Closing lobbies abandons undelivered
   replies and pushes; document this as accepted for operator shutdown.
3. Stop and wait for reaper, relay sockets and JWKS refresh through explicit
   owners. Fix the existing blockers first: NAT loops on any read error
   (`nat.go`), the PROXY listener can block on `l.conns <- pc`
   (`proxyproto.go`). Register connections before starting their goroutine.
4. Orderly path: workers and callbacks done, then close SQLite, then release
   the instance lock. One overall drain limit includes `DB.Close`. On expiry the
   entrypoint exits forcibly without releasing the lock early while goroutines
   still run. Never delete the WAL manually.

Durability statement to document: each SQLite transaction is atomic and durable
at commit (WAL, `synchronous=FULL`); success replies follow commits. Atomicity
is per transaction, not per task (125/1 runs race rewards, refresh and
challenges separately), and a commit can occur without the client receiving the
reply: no exactly-once promise. Transaction boundaries do not change.

Startup errors after resources are open return instead of `log.Fatalf`.
Established sessions keep no idle or elapsed-time cutoff. Also: component log
prefixes (`[auth]`, `[lobby]`, `[ctr]`...) instead of `[D3]`, redaction
unchanged; remove stale headers (`main.go` lists `bdcrypto.go`, `auth.go`
describes Diablo III Switch); update README, FORK.md and the diagram in
`DEMONWARE_PROTOCOL.md`; add a linter configuration only if available offline.

## Open question recorded, not changed by this refactor

Identity double read: `candidates` admits keys only from a fresh locally issued
ticket (`issuedTicketPlayer`), but `loadIdentity` looks the ticket up again
after keys are installed. If an intervening `rememberTicket` prunes it (it needs
the one-minute grace to elapse between the two reads), the session holds valid
keys without a player. Keys still derive only from a validly issued ticket, so
this is not an admission bypass. Characterize it with a controlled test and
report it; add no new admission rule here.

## Stop conditions (stop and report instead of continuing)

- A Phase 0 fixture changes outside the documented Phase 2 Diablo entries.
- Preserving behavior would require changing a protected contract.
- Evidence suggests CTR depends on behavior this plan would disable, or a
  behavior's Diablo-only classification is uncertain.
- The refactor needs a new process, service, third-party module or network API.

## Final report for review

Per phase: commits, what moved, the inventory matrix, fixture changes with
reasons, test counts, vet/build results, re-run fuzzing of `internal/demonware`,
the open question above and remaining risks. Expected size: about 3, 4, 3, 6-8
and 3 commits for Phases 0-4. The riskiest step is the session, matchmaker and
callback extraction. Then the user decides installation and the in-game
acceptance of both instances: shadNet A/B (normal) and guest C/D (unlock-all).
