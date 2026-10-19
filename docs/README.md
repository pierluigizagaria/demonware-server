# Documentation

## Using the server

- [Configuration](CONFIGURATION.md): admission, listeners, economy and storage.
- [Complete settings reference](../example.env).
- [Building and testing](BUILDING.md): independent builds and automated checks.
- [Current limitations](LIMITATIONS.md): incomplete features and supported scope.
- [Credits](../CREDITS.md) and [license](../LICENSE.md).

## Integration and implementation

- [Local publisher and P2P boundary](P2P_LOCAL.md): inherited owner, capabilities,
  room messages, endpoints and lifecycle.
- [Verified Steam admission](STEAM_ADMISSION.md): central helper-based admission.
- [Demonware protocol](DEMONWARE_PROTOCOL.md): wire contracts and compatibility.
- [CTR challenges](../CTR-CHALLENGES.md): rules and source evidence.
- [Implementation and provenance](../FORK.md): component responsibilities and
  upstream lineage.

The Go protocol package is internal to this module. Other applications integrate
through the documented network and companion boundaries, not sibling sources or
private runtime state.
