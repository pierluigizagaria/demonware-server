# Building and testing

## Requirements

- Go 1.25 or newer, as specified in [go.mod](../go.mod).
- The dependencies pinned in `go.mod` and `go.sum`.

SQLite uses the pure-Go `modernc.org/sqlite` driver. No C compiler, OpenSSL,
Steam SDK, game assets or sibling repository is required to build the server or
run its ordinary tests.

## Build

Run from the repository root:

```sh
go build -o bin/demonware-server ./cmd/ctrnf-server
```

Windows:

```powershell
go build -o bin/demonware-server.exe ./cmd/ctrnf-server
```

The command remains `cmd/ctrnf-server` because CTR:NF is the admitted title.
The module is `github.com/pierluigizagaria/demonware-server`. Build output under
`bin/` is ignored by Git. The CTR catalog, challenge definitions and reward tables
are embedded in the executable; optional physical publisher files are separate.

For an offline build, provision a compatible Go toolchain and its module cache
first, then set `GOPROXY=off`. This repository does not supply a toolchain or
download dependencies from sibling projects.

## Checks

```sh
go vet ./...
go test -count=1 ./...
```

Tests use synthetic inputs and temporary state. Unit tests live beside their
owners; `cmd/ctrnf-server` also tests composition, admission, instance isolation
and shutdown. Fixed dispatcher snapshots and JSON references protect wire and
persistence compatibility. See the
[JSON inventory](DEMONWARE_PROTOCOL.md#json-compatibility-references) before
changing a serialized field.

A bounded wire-parser fuzz run:

```sh
go test ./internal/demonware -run '^$' -fuzz '^FuzzWireParsers$' -fuzztime=15s
```

## Optional integration tests

These require explicit external setup and are separate from ordinary tests:

- **shadNet issuer:** set `SHADNET_INTEROP_FIXTURE` to the private fixture expected
  by `cmd/ctrnf-server/platform_shadnet_integration_test.go`, then run
  `go test -tags=integration -run TestShadNetPublisherInterop ./cmd/ctrnf-server`.
- **Steam helper:** follow [Verified Steam admission](STEAM_ADMISSION.md) to
  provide the native helper, client and an existing Steam session.
- **P2P contracts:** [tools/p2p_probe](../tools/p2p_probe/README.md) documents
  optional provider integration and owned-image probes.

Keep credentials, filled env files, captures and databases out of commits and
release packages. Fixture success demonstrates the tested contract; original
game behavior and multi-machine transport require separate validation.