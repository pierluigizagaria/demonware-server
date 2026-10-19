# Demonware Server

An independent publisher server for **Crash Team Racing Nitro-Fueled**, derived
from the Nextendo Demonware server. It implements the PS4 protocol used by
**CUSA13795 01.21** (Demonware title **5760**), for compatible emulator and native
port integrations.

One Go executable provides authentication, encrypted lobby sessions, matchmaking,
Pit Stop inventory, progression, leaderboards and NAT discovery. It can run as a
central server or as a local publisher managed by a platform provider. Game
execution and platform services belong to separate projects.

**Experimental:** client integration is required. Automated tests do not establish
compatibility with every host or qualify Internet multiplayer or public hosting.
See [Current limitations](docs/LIMITATIONS.md). Diablo III code is retained only
as an inactive compatibility reference.

## Features

- Explicit shadNet, Steam, local-companion or anonymous admission.
- Central matchmaking, or provider-backed peer matchmaking with local progression.
- Normal progression and an opt-in unlock-all Pit Stop policy.
- SQLite persistence, configurable menu news and monitoring endpoints.
- Graceful shutdown with no idle cutoff for established lobby sessions.

## Build

Windows preview binaries are available in
[Releases](https://github.com/pierluigizagaria/demonware-server/releases).
For the native P2P mod, use the combined
[ctrnf-p2p package](https://github.com/pierluigizagaria/ctrnf-p2p/releases) and its
installation instructions.

Requires **Go 1.25 or newer**. No C compiler, OpenSSL or sibling checkout is needed.

```sh
git clone https://github.com/pierluigizagaria/demonware-server.git
cd demonware-server
go build -o bin/demonware-server ./cmd/ctrnf-server
```

On Windows, use `-o bin/demonware-server.exe`. See
[Building and testing](docs/BUILDING.md) for checks and optional integration tests.

## Run

Copy [example.env](example.env) to a private instance file, configure the selected
admission provider, TLS and listener addresses, then run:

```sh
./bin/demonware-server -env server.env
```

The default is **shadNet admission, central matchmaking and normal progression**.
It requires a configured shadNet issuer and a compatible client; the example is a
settings reference, not a ready-to-connect deployment.

| Integration | Admission | What starts the server |
| --- | --- | --- |
| Central server with shadNet accounts | `shadnet` | Operator |
| Central server with verified Steam tickets | `steam` | Operator; server starts the separate native verification helper |
| Local publisher with Steam peer transport | `local` | A compatible companion provider, such as orbiSteamworks |
| Anonymous unlock-all instance | `guest` | Operator; requires an account-free client integration |

All authentication replies are unsigned. The client must enable the
[CTR:NF Server Signature Patch](https://github.com/pierluigizagaria/ctrnf-server-signature-patch).
The server includes neither a game client nor its routing and platform integration.
For local P2P, use the host/bridge's setup instructions: `CTR_ADMISSION=local`
requires a private inherited-pipe handshake and cannot be started as a standalone
replacement for that provider.

See [Configuration](docs/CONFIGURATION.md) for provider prerequisites, defaults,
database ownership and the difference between central and local economy modes.

## Integration and development

The server builds and tests independently. Its external boundaries are Demonware
client traffic and the documented companion protocol; it does not call the Steam
SDK or import a host's implementation.

`internal/demonware` separates wire framing, crypto and typed buffers from CTR
rules. It is an internal Go package, **not a public SDK** or a promise of support
for other games. See the [protocol contract](docs/DEMONWARE_PROTOCOL.md) before
changing serialized data, and the [documentation index](docs/README.md) for
integration contracts, architecture and historical validation records.

## License and credits

This project retains the **[PolyForm Shield License 1.0.0](LICENSE.md)**, including
its Noncompete terms. It is source-available software. See [CREDITS.md](CREDITS.md)
for original authors, contributions, dependencies and research references.

It is unofficial and is not affiliated with Sony, Activision, Demonware,
Nintendo, Nextendo Network or the shadPS4 project.

Required Notice: Copyright 2026 Nextendo Network
