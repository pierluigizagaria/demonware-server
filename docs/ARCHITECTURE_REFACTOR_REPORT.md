# Architecture refactor implementation report

Review handoff for the user and Claude, 2026-10-14. Phases 0–4 of
[the binding brief](ARCHITECTURE_REFACTOR.md) are implemented on the local
`refactor/ctrnf-architecture` branch, based on `33dc1c6`.
`feat/ps4-platform` remains at `33dc1c64767aeb4ebb3ab21bdd201f208950a075`.
This report accompanies the final log/documentation finishing commit; the
implementation commits through lifecycle completion are listed below.

No push, lab installation, service/profile/game launch or dependency installation
was performed. Tests used synthetic inputs, temporary databases, `net.Pipe` and
local test listeners/`httptest`. No production Sony/Demonware request or private
lab credential was used. AGENTS.md was read and left unchanged. The first commit
tracks the previously untracked brief; Phase 0 adds its inventory matrix.

## Phases and commits

Each listed commit has a message body and passed formatting, vet and the complete
uncached test suite before commit. Test counts are named tests/subtests, not
assertion counts. `go test` compiles every package; the standalone executable
build was performed at the final handoff, not claimed separately for each phase.

| Phase | Commits and changes | Acceptance |
| --- | --- | --- |
| 0: records and safety net | `cae0bbf` tracks the brief and accepted protocol baseline; `70ccd7c` characterizes dispatcher frames and identity reads; `32b0c55` adds workflows and the inventory; `c17fc61` pins the baseline achievement clock | 1,035 tests; vet and gofmt pass; 648 original dispatcher snapshots |
| 1: instance ownership | `ae6861a` fresh configuration defaults; `c532ae3` ticket owner; `5b585a5` presence/names; `ecfb9b6` reply counters; `c8ad98e` economy/stats/news dependencies; `49ac6bc` matchmaking registries/counters; `1c20cd1` runtime/settings/diagnostics owners; `a4d81be` composed isolated instances | 1,044 tests; vet and gofmt pass; equal numeric identities remain isolated across instances |
| 2: dispatch and Diablo boundary | `9f42a4c` request-owned replies/callbacks; `05749c1` narrow session dependencies; `8473560` connection-ID peers; `1587651` one instance service table; `6bfc544` context-specific parsers; `4a1e93f` disconnects Diablo paths | 1,050 tests; vet and gofmt pass; only the twelve documented Diablo snapshot overrides |
| 3: packages | `d271197` monitoring views; `7d327fd` transport sessions without game handles; `7e1a1ae` component contracts; `b48f6b8` adapters/reference views; `bd1f6a3` mechanical package extraction | 1,050 tests; vet and gofmt pass; resources, SQL and snapshot bytes unchanged |
| 4: lifecycle and finish | `b7e85d7` owned background workers; `7a60fa7` lobby admission/drain; `dbd8e5d` HTTP and entrypoint shutdown; `670541f` retains the shadNet provider until auth handlers drain; the finishing commit containing this report updates log prefixes, headers and documentation | 1,072 tests; vet, gofmt, executable build and bounded protocol fuzzing pass |

Commit granularity exceeds the brief's approximate counts because dependency
seams were split from mechanical moves and each ownership change was verified
independently. The extraction commit contains no intentional behavioral change.

Phase 0 also updated the workspace documents outside this repository:
`../docs/START_HERE.md`, `../docs/SHAREABLE_PLAN.md` and
`../docs/multiplayer_plan.json`. The accepted run is **33dc1c6 with shadNet A/B**
on 2026-10-14: shared lobby, races through results, matched searches, accepted
syncs, leaderboard uploads and persisted rewards, without MAC errors. Guest C/D
remained on the prior build. The single profile-B `Game:Main`/`0xc0000005` crash
roughly five seconds after a match notification remains unattributed; it did
not recur after restart.

## Inventory and compatibility evidence

The complete pair-only matrix is in
[Phase 0 inventory](ARCHITECTURE_REFACTOR.md#phase-0-inventory-2026-10-14):
service/task, F/C/U/H/P/S/I source class, observed build/run segment, baseline
behavior and fixture. It contains no identities. Log segments are distinguished
by build/run, including the historical 104/5 default response on 2026-10-12.
Ambiguous entries stay ambiguous; absence from a log never establishes disuse.

The 648-snapshot JSON remains byte-identical to its initial commit, now at
`cmd/ctrnf-server/testdata/architecture_dispatch_baseline.json`. Frames are
compared after MAC/sequence verification, using inner type and plaintext, not
random ciphertext. The harness includes malformed/default branches, selected
write failures, pushes and state effects, supplemented by successful workflows
and persistence tests. Auth tests verify a synthetic response signature before
inspecting or normalizing its body.

Only these four prefixes have different expected results, each under
`/write-fail-0`, `/write-fail-1` and `/write-fail-2` (twelve IDs total):

| Prefix | Reason and result |
| --- | --- |
| `021-001-empty` | Inactive Diablo create: prior error becomes unknown-service empty success |
| `021-001-malformed` | Same source-proven Diablo create disconnection |
| `021-019-empty` | Inactive Diablo reservation: empty success with no registry allocation |
| `021-019-malformed` | Same reservation disconnection; the old handler ignored these arguments |

`architecture_diablo_test.go` declares these overrides; the original JSON is
not regenerated. All other 636 snapshots retain their original bytes and state.
`TestArchitectureContextRegistryLifecycle/21` explicitly selects the inactive
Diablo compatibility table; `/138` exercises the live CTR adapter. Generic
publisher storage 10/21 remains, including a case-insensitive physical-file test.

The Phase 0 125/3 fixture originally crossed a daily boundary when run later in
the day. `c17fc61` fixes its clock to its recorded period (2026-10-14 14:00 UTC).
It changes neither the recorded snapshot nor production time/reward behavior;
timestamps used by receipts, validity and multipliers are not normalized away.

The [Diablo fate ledger](ARCHITECTURE_REFACTOR.md#phase-2-diablo-disconnection-and-fixture-ledger)
covers service 21, generation, aliases, physical outputs, CLI, dashboard and
environment settings. Existing physical files were not deleted. Services 138,
29 and 68 retain their shared functionality. `D3_PUBFILES_CONFIG` and
`D3_RIFTDATA` remain accepted but inactive; `D3_PUBFILES` still locates generic
CTR physical files. `pubfiles` validates and exits without writes; `/pubfiles/`
has no route. No other admitted-title behavior was deliberately disabled.

## Package moves and boundaries

| Destination | What moved or became owned |
| --- | --- |
| `cmd/ctrnf-server` | CLI, immutable configuration loading, instance composition, startup/shutdown and integration fixtures |
| `internal/config` | Environment/file parsing, validation and news configuration |
| `internal/auth` | HTTPS auth, ticket directory, response signing and PROXY listener |
| `internal/platform`, `/ps4`, `/guest`, `/shadnet` | Identity contracts, PS4 envelope, explicit guest provider and the existing native shadNet verifier/JWKS owner |
| `internal/lobby` | Listeners, handshake, session keys/sequences, request router/contract, reply counters and shared contextual registry |
| `internal/ctr` | Cohesive economy/reward/challenge/marketplace/stats/news/achievement logic, engine, CTR adapters, Umbrella and four embedded JSON resources |
| `internal/store` | SQLite adapter and OS instance lock; implements persistence interfaces consumed by CTR |
| `internal/presence` | Names, friends, opaque user data, rich presence and consumer contracts |
| `internal/nat`, `/relay`, `/dashboard` | Owned network workers and telemetry; dashboard receives snapshots/callbacks |
| `internal/diagnostics` | Instance redaction, verbosity and capture settings |
| `internal/httpserver` | Admission and drain for the existing auth/dashboard HTTP transports; no additional endpoint/listener |
| `internal/diablo` | Inactive service table, D3 parser, publisher files, seasons, rifts, embedded configuration and generator tests |
| `internal/demonware` | Unchanged shared protocol package and fixed vectors |

Production dependency inspection confirms `lobby` does not import `ctr`, `ctr`
does not import `store`, and the executable has no dependency path to `diablo`.
There is still one module, one executable and no new third-party dependency or
network API. `go.mod`, `go.sum`, `LICENSE.md` and `internal/demonware` are unchanged
from `33dc1c6`. All four CTR JSON resources are byte-identical after moving.
All 42 raw SQLite literals match the baseline; schema version 1, transaction
boundaries, admission namespaces and mode checks are unchanged.

Handlers receive narrow dependencies, never the composed instance. Peer identity
is keyed by `ConnID`, retaining stale-disconnect protection. Matchmaking notices
are built under the engine lock and sent afterward. `AfterReply(sendErr)` belongs
to one request and executes once after its send attempt, including failure:
145/18 releases a failed reservation; marketplace/achievement callbacks retain
their existing behavior of ignoring that error.

The original router ordering and defaults remain covered: 18-byte special case
first; no response below three bytes; unreadable 80/104 tasks fail while others
return empty success; unknown service returns empty success; service 4 without
identity returns router-level empty success. Per-service defaults are unchanged.

## Shutdown and durability

SIGINT/SIGTERM and partial-startup failures use the same resource owner:

1. Close lobby/HTTP admission, including keep-alive requests.
2. Interrupt idle/partial lobby reads; drain admitted tasks, replies and callbacks.
3. Stop and join registry reaping, NAT, relay, PROXY and JWKS workers.
4. Close SQLite, then release the OS instance lock.

The shadNet verifier remains available until admitted HTTP handlers finish;
then its outstanding background refresh is cancelled and joined, still before
SQLite closes and within the same overall budget.

The 30-second budget covers all stages, including `DB.Close`. A timeout returns
to the entrypoint's forced process exit, without a deferred cleanup releasing
the lock under running workers. The WAL is never manually removed. Operator
shutdown can abandon undelivered replies/pushes; established sessions retain
no automatic idle, menu or elapsed-time cutoff. Existing per-request network
deadlines remain in force.

Fixtures cover successful and failed in-flight reply sends, callback cardinality,
idle/partial reads, registration before worker launch, HTTP admission/drain,
NAT read-error exit, pending PROXY handoff cancellation, reaper/relay stop,
JWKS refresh cancellation/join, startup failure cleanup, timeout while a worker
is active, timeout inside database close and a real OS lock held until the
pool's close finishes. Tests do not claim a live-process signal or game session
qualification.

SQLite transactions are atomic and durable at commit (WAL,
`synchronous=FULL`). Success replies follow commits, but a commit can survive
without its reply reaching the client. Atomicity is per transaction, not per
task: 125/1 still commits race rewards, refresh and challenges separately.
There is no exactly-once delivery promise and no new SQL context/cancellation.

## Final verification

Local toolchain: **Go 1.27.1, windows/amd64**, with repo-local
`GOCACHE=.cache/go-build` and `GOPROXY=off`. Existing Git-bundled OpenSSL was put
on the verification process's PATH so signing tests ran instead of skipping.

- `gofmt`: clean.
- `go vet ./...`: passed.
- `go test -count=1 -json ./...`: **1,072 named tests/subtests passed, zero
  failures and zero skips**.
- `go build -o .cache/architecture-refactor/ctrnf-server.exe ./cmd/ctrnf-server`:
  passed; executable remains under `.cache`, not installed or run.
- `go test ./internal/demonware -run '^$' -fuzz '^FuzzWireParsers$' -fuzztime=15s`:
  passed; the final run completed **493,055 executions** after 49 seed/cache
  cases, with no failure (16.511 seconds including harness overhead).
- Dependency boundaries, original dispatcher JSON, protocol files, module files,
  license, embedded resources, SQL literals and example-env assignments checked.
- No offline `golangci-lint` or `staticcheck` executable was found; no linter
  configuration or installation was added. The race detector was not run:
  `CGO_ENABLED=0` and no configured C compiler on PATH.

Detailed machine logs and generated binaries remain private under
`.cache/architecture-refactor`; none is committed. The verification command
uses a stable script prefix for reusable approval where the host policy permits.
No global approval/security setting was changed.

## Identity double read and remaining review

`TestArchitectureIdentityDoubleRead` preserves the recorded behavior:
`Candidates` obtains keys only from a fresh locally issued ticket. If a later
`RememberTicket` prunes that ticket after the validity window plus its one-minute
identity grace, `LoadIdentity` can miss the second lookup even though the session
already holds keys derived from that validly issued ticket. This is not a new
admission bypass: a new connection cannot admit that pruned ticket. The refactor
adds no identity/admission rule to resolve this open question.

The architecture branch still requires the user/Claude final review. Installation
and in-game acceptance are separate future decisions for A/B (normal shadNet)
and C/D (guest unlock-all). LAN/WAN, relay suitability, public hosting, capacity
and live Diablo compatibility remain unqualified. Client-reported rewards do
not establish authoritative race validation. The historical profile-B crash
remains open and unattributed. These limits are not inferred failures of the
refactor, and passing synthetic tests does not remove them.

No binding stop condition required changing a protected contract. Development
fixture/wiring failures were corrected before their commits; the only intended
baseline result changes are the twelve source-classified Diablo entries above.
