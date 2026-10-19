# Verified Steam admission

`CTR_ADMISSION=steam` admits players on a central server by verified Steam
identity, using the orbiSteamworks
[authentication helper](https://github.com/pierluigizagaria/orbisteamworks/blob/main/server/auth-helper/README.md)
over private child-process pipes. It is separate from local P2P admission
(see [Local publisher and P2P boundary](P2P_LOCAL.md)).

**Client availability:** this mode needs a game client that obtains `osw1`
tickets from orbiSteamworks and sends them to this server. No such client is
published yet; see [Current limitations](LIMITATIONS.md#client-integrations).

## Configuration

Set `CTR_ADMISSION=steam`, an absolute `CTR_STEAM_HELPER` executable path and
`CTR_STEAM_REALM` (1–64 letters, digits, underscores or hyphens). Optionally set
`CTR_STEAM_LIBRARY` to the operator-supplied Steam API library; otherwise the
helper loads it from its own directory. Leave all `SHADNET_*` settings empty.
The AppID is always 480, and no Steam Web API key or client password is used.

Use a fresh `DATA_DIR`. The database is bound to the trust ID
`ctrnf:steam:480:5760:v1:<realm>` and to the instance's normal or unlock-all
policy; shadNet or guest databases are refused and are not migrated.

Authentication replies are unsigned, so clients need the
[CTR:NF Server Signature Patch](https://github.com/pierluigizagaria/ctrnf-server-signature-patch).

## Admission flow

The token format, helper protocol and verifier rules are specified in
[osw1 token and auth-helper protocol](https://github.com/pierluigizagaria/orbisteamworks/blob/main/docs/OSW1_TOKEN_AND_HELPER.md).
This server implements them as follows.

1. **Discovery.** `GET /orbi/steam/v1` on the auth HTTPS listener returns the
   protocol, AppID, title, realm and current helper server ID, with
   `Cache-Control: no-store`. It carries no credential and is absent with other
   admission providers. The helper identity changes when it restarts. Clients
   must validate the server's TLS identity and realm before requesting a bound
   ticket.
2. **Envelope.** The client sends `osw1.<SteamID>.<hex-ticket>.<base64url-online-ID>`
   inside the original PS4 authentication envelope: `title_id` `5760`,
   `auth_task` `46`, a decimal `iv_seed`, and `extra_data` → `token` →
   `id_token`, in a body of at most 16 KiB.
3. **Verification.** The helper validates the ticket through anonymous Steam
   game-server authentication. The server requires the validated SteamID to
   match the token and a nonzero owner (owner and user may differ), then records
   the ticket's SHA-256 hash and the SteamID/name binding in one transaction.
4. **Identity.** The player is admitted with PID = SteamID64, the online ID as
   username, subject `steam:<SteamID64>` and kind `steam`.

Names are 1–16 bytes of UTF-8 without control characters or surrounding spaces;
the client derives them from the Steam persona. They are unique regardless of
letter case. The same SteamID may present a new name on a later admission; the
binding, the name used by friend lookups and the player's leaderboard rows
follow it. A name conflict, replay, unavailable store or helper failure refuses
admission. There is no unverified Steam mode.

## Lifetime

Consumed ticket hashes never expire; Steam decides ticket validity. A verified
admission must be claimed by the game's lobby connection within the 120-second
handoff window for issued lobby tickets, otherwise it expires. Each admission is
claimed once, and closing that connection releases it, as do authentication or
response-write failures. Later Steam revocation or helper loss does not
disconnect an established player. After a helper failure the server refuses new
admissions until the operator restarts it.

Matchmaking, room rules and the Demonware wire protocol are the same as for
other admission providers.
