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
- **Steam helper:** `internal/platform/steam` contains an opt-in integration
  test for the helper protocol. It requires explicit native artifacts and an
  existing Steam session; ordinary tests do not initialize Steam.

Keep credentials, filled env files, captures and databases out of commits and
release packages. Fixture success demonstrates the tested contract; original
game behavior and multi-machine transport require separate validation.

## Automated Windows builds and releases

GitHub Actions runs the ordinary offline tests on pull requests and pushes to
`main`. Builds use Windows Server 2022, Python 3.11.9 and Go 1.27.1. Hosted runner images
can change; manifests record their image version and source commit. This is
dependency pinning and traceability, not a claim of bit-identical compiler output.

Open **Actions → Build, test and prepare release → Run workflow**, select `main`, and enter
a version such as `v0.1.0-preview.1`. With `create_draft` selected, successful
checks create a **draft prerelease** with the ZIP and external SHA-256 checksum.
An existing release or conflicting tag is never replaced. For a packaging-only
trial, clear `create_draft` and download the ZIP from the run's Artifacts.
No new secret or personal token is required; only the draft job has write access.

Inspect and test the downloaded package before publishing the draft. Offline
fixtures do not establish live Steam or multi-PC game acceptance. Keep user
configuration, databases, installed profiles and game files out of packages.

Run the same build locally from the repository root with CMake/Go and Python on
PATH: `python tools/ci_build.py --version v0.1.0-preview.1`.
Builds use ignored output directories and refuse to overwrite an existing ZIP;
choose a fresh version or archive the earlier output before rebuilding.
The existing first preview release is not changed by adding these workflows.

Go dependencies remain in `go.mod`/`go.sum`. CI verifies modules, runs `go vet`
and all ordinary tests with readonly module resolution, then builds with
`-trimpath`. Packages collect license notices from the command's linked modules
and the Go toolchain. Dependabot proposes Go and GitHub Actions updates as PRs.
