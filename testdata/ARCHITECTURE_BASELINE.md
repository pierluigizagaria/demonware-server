# Dispatcher baseline

`architecture_dispatch_baseline.json` was recorded with the application code
unchanged from `33dc1c6`. The fixture is synthetic and contains no live identities,
tickets, keys or ciphertext. `TestArchitectureDispatcherBaseline` covers every
handled selector, every service default, short requests, unreadable task tags,
and malformed argument headers, plus selected valid stateful requests. Each case
runs with successful writes, a failed first write and a failed second write.

Every attempted frame (including a failed write) is checked for its length,
server MAC, sequence, decrypted inner type and payload. Counter increments,
registry sizes, callback consumption, send errors and SQLite row counts are
recorded. Existing domain fixtures retain detailed persistence, request-layout,
matchmaking, presence, news, reward and replay assertions; their dispatcher frame
decoder now authenticates all frames and checks sequences before decryption.

The only normalization in this baseline is:

- 12/6: its four-byte time result, after checking the actual call interval.
- 21/19 and 138/19: the returned eight-byte random reservation ID, after checking
  the exact ID, owner, reservation flag and call-time timestamp in the registry.

Transactions start from a known fixture-owned counter and are not normalized.
Reward timestamps are never normalized. No global entropy source is replaced.
`architecture_workflows_test.go` additionally checks successful dispatcher paths:
stats persistence and reads, exact reward receipts and audit timestamps, callbacks
after failed replies, context-registry lifecycle, search/cancel, pair creation,
host sync and guest push, documents/disband, names, user data and presence.
Component-local clock/entropy seams make the time-dependent workflows repeatable;
the default clock/entropy call sites, SQL and transaction boundaries are unchanged.
The original baseline JSON remains unchanged. The golden header fixture alone
does not claim to cover every successful argument branch.

`TestArchitectureIdentityDoubleRead` captures the documented split between key
candidate admission and the subsequent identity lookup, with an intervening
pruning operation whose explicit time crosses the grace period. It does not
introduce a new admission rule. `TestArchitectureAuthSignatureBeforeInspection`
uses a synthetic provider, ephemeral key and `httptest` response; the signature
is verified on the original body before JSON inspection.

Baseline validation: 370 existing named tests/subtests passed. With these
characterizations: 1,022 passed; no failures or skips. `go vet ./...` passed.
The Windows sandbox prevented some temporary-file renames and loopback HTTP;
the full suite was therefore run outside it with the same local Go toolchain,
`GOCACHE=.cache/go-build`, `GOPROXY=off` and installed Git OpenSSL on PATH.
No laboratory service, profile or game was started or changed.
