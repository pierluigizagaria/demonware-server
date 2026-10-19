# Credits and provenance

Demonware Server is maintained as an independent repository by
[Pierluigi Zagaria](https://github.com/pierluigizagaria). Its history and source
derive from the projects below. Repository independence does not imply original
authorship of the inherited implementation or a change of license.

## Original server and CTR implementation

- **[nxmod / nx-mod](https://github.com/nx-mod)** created the original Diablo III
  Demonware server: [original repository](https://github.com/nx-mod/nextendo-demonware).
- **[Nextendo Network](https://nextendo.network)** and
  **[NextendoNetwork](https://github.com/NextendoNetwork)** developed the online
  stack and server integration from which this code descends, including the
  account, routing, presence and monitoring patterns. The pinned source baseline
  is [NextendoNetwork/nextendo-demonware](https://github.com/NextendoNetwork/nextendo-demonware)
  commit `c3400c312a739cf7598204a981156959910ed874`.
- **[CollectingW](https://github.com/CollectingW)** contributed the original
  Crash Team Racing Nitro-Fueled support and its Demonware reverse engineering:
  title-aware login, signed auth replies, Umbrella tokens, contextual/async
  matchmaking, friend sessions, rich presence and tests. This work originated
  in the [`crash-team-racing` branch](https://github.com/CollectingW/diablo-3/tree/crash-team-racing)
  and pull request #1 of `nx-mod/diablo-3`.

The retained Git history records individual contributions. This project's PS4
adaptation, shadNet/guest admission, SQLite integration and architecture work
build on that foundation. Development includes AI-assisted implementation with
Codex and architecture discussion/review with Claude.

## Related PS4 projects

- **[shadPS4](https://github.com/shadps4-emu/shadPS4)** provides the independent
  PS4 emulator integration.
- **[shadNet](https://github.com/shadps4-emu/shadNet)** provides the independent
  account/identity service used by normal instances.
- **[CTR:NF Server Signature Patch](https://github.com/pierluigizagaria/ctrnf-server-signature-patch)**
  is the separate client-side signature-trust prerequisite for the tested PS4 game.

These projects retain their own authorship and licenses. Listing them does not
imply endorsement or that their source is included in this repository.

## Research references retained from upstream

The original documentation credits the following instrumentation and protocol
references. These are upstream provenance claims, not a claim of newly verified
PS4 behavior or a blanket license assessment of the referenced projects.

- **[D3Hack / D3StudioFork](https://github.com/god-jester/D3StudioFork)** by
  **jester**, built on **[exlaunch](https://github.com/shadowninja108/exlaunch)**
  by **Shadow**: instrumentation/hooks and Diablo publisher-file format references.
- **[project-bo4/shield-development](https://github.com/project-bo4/shield-development)**
  (GPL-3.0): Demonware NAT-discovery packets and service identifiers.
- **[Laupetin/open-bitdemon-emulator](https://github.com/Laupetin/open-bitdemon-emulator)**
  (AGPL-3.0): standalone backend and service-result layout references.
- **[Ezz-lol/boiii-free](https://github.com/Ezz-lol/boiii-free)** (GPL-3.0):
  lobby/service emulation and `bdMatchMakingInfo` layout references.
- **[Protarium-Network/bo2-wiiu-demonware](https://github.com/Protarium-Network/bo2-wiiu-demonware)**:
  NAT introduction packet format.
- **[skkuull/dwd3](https://github.com/skkuull/dwd3)** (GPL-3.0),
  **[jordam/demonbugger](https://github.com/jordam/demonbugger)** and
  **[hosseinpourziyaie/demonware-companion](https://github.com/hosseinpourziyaie/demonware-companion)**:
  Demonware reverse-engineering tools.

[The protocol contract](docs/DEMONWARE_PROTOCOL.md) distinguishes inherited
upstream reconstruction from original PS4 evidence and replacement policy.

## Dependencies and notices

Go dependency versions remain pinned in `go.mod` and `go.sum`, including the
`modernc.org/sqlite` driver and `golang.org/x/sys`. Their source and licenses
belong to their respective authors. No game assets, firmware, certificates or private
account data are distributed with this project.

The original [PolyForm Shield License 1.0.0](LICENSE.md) is retained verbatim.

Required Notice: Copyright 2026 Nextendo Network
