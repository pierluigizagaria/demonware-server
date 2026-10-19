# Documentation

## Using the server

- [Configuration](CONFIGURATION.md): admission, listeners, economy and storage.
- [Complete settings reference](../example.env).
- [Building and testing](BUILDING.md): independent build and optional integrations.
- [Current limitations](LIMITATIONS.md): compatibility replies, incomplete features
  and deliberate scope.
- [Credits and dependency notices](../CREDITS.md).
- [License](../LICENSE.md).

## Integration contracts

- [Local publisher and P2P boundary](P2P_LOCAL.md): inherited owner, capabilities,
  room messages, endpoints and lifecycle.
- [Verified Steam admission](STEAM_ADMISSION.md): central helper-based admission.
- [Demonware protocol](DEMONWARE_PROTOCOL.md): wire contracts, evidence and JSON
  compatibility references. The Go package remains internal to this module.
- [CTR challenges](../CTR-CHALLENGES.md): rules and source evidence.

## Design and history

These records preserve the decisions and observations of their stated revisions.
Old branch names and local test setups are historical references, not installation
requirements. Use the configuration guide for current operator instructions.

- [Implementation and upstream provenance](../FORK.md).
- [Architecture refactor plan](ARCHITECTURE_REFACTOR.md) and
  [implementation report](ARCHITECTURE_REFACTOR_REPORT.md).
- [Repository transition](REPOSITORY_TRANSITION.md).
- [P2P NAT investigation](P2P_NAT_INVESTIGATION.md).
- [Optional P2P probes](../tools/p2p_probe/README.md).
- [Historical Switch README](HISTORICAL_SWITCH_README.md),
  [status assessment](../STATUS.md), [Diablo notes](../NOTES.md),
  [publisher-file notes](../PUBFILES.md) and [season notes](../SEASONS.md).

Historical client observations and automated fixture results apply only to the
versions and conditions recorded there. Neither implies general host, Internet
multiplayer or public-service acceptance.
