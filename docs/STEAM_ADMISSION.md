# Verified Steam admission

This is the central `CTR_ADMISSION=steam` integration. It remains fixed to AppID
480 and differs from provider-managed local P2P, whose AppID is supplied through
the companion protocol; see [Configuration](CONFIGURATION.md#admission-and-matchmaking)
and [P2P_LOCAL.md](P2P_LOCAL.md). Current authentication replies are unsigned and
require the client signature patch.

The `steam` admission provider uses the orbiSteamworks authentication helper
over private child-process pipes.

Use `CTR_ADMISSION=steam`, an absolute `CTR_STEAM_HELPER` executable path and
`CTR_STEAM_REALM` (1–64 letters, digits, underscores or hyphens). Optionally set
`CTR_STEAM_LIBRARY` to the user-supplied Valve runtime library; otherwise it is
loaded beside the helper. Leave all `SHADNET_*` settings empty. AppID is fixed
at 480. No Steam Web API key or client password is used.

Select a fresh `DATA_DIR`. Trust is `ctrnf:steam:480:5760:v1:<realm>` and the
instance's normal/unlock-all policy is still enforced separately. Existing
shadNet or guest databases are refused and are not migrated. The initial
SQLite schema includes Steam name bindings and consumed ticket hashes.

The Steam client issues `osw1.<SteamID>.<hex-ticket>.<base64url-online-ID>`.
The original PS4 publisher envelope remains unchanged. The native helper
checks the ticket using anonymous game-server authentication. The provider
accepts only a matching validated SteamID and a nonzero owner, then atomically
records the ticket's canonical SHA-256 hash and the SteamID/name binding.
Names are 1–16 bytes of UTF-8 without control characters or surrounding
spaces (the client derives them from the Steam persona). They are
case-insensitively unique. The same SteamID may present a new name on a later
admission: the binding, the 12/10 name and its leaderboard rows follow it. A name conflict, replay, unavailable store or helper failure
refuses admission. No unverified Steam mode is implemented.

Consumed hashes persist without a short expiry. Native Steam determines
ticket validity; a verified 120-second Steam ticket age is not claimed.
The existing 120-second publisher-ticket window is only a handoff deadline:
an unclaimed native admission expires, while a claimed lobby connection has
no elapsed-time or idle timeout. Each native admission can be claimed once.
Connection close releases it; authentication/response-write failures release
it too. Later Steam revocation or helper loss never disconnects an established
publisher player. A failed helper generation refuses new admissions until
the operator restarts the server.

`GET /orbi/steam/v1` on the existing publisher HTTPS listener returns the
protocol, AppID, title, realm and current helper server ID. It carries no
credential and is marked `Cache-Control: no-store`. Hosts must validate the
configured publisher's TLS identity and realm before issuing a bound ticket.
The helper identity can change on restart. The endpoint is absent with other
admission providers. Binding does not authenticate the online-ID field; the
publisher's persistent binding owns that decision.

Matchmaking, room rules and the shared Demonware wire protocol are unchanged.
The lobby handshake gains only native-admission ownership at the existing
issued-ticket boundary. Default shadNet and guest providers use no native
admission object and keep their existing behavior.
