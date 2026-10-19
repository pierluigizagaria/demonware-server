# Configuration

[example.env](../example.env) lists the accepted settings. The server reads them
once at startup, directly from its process environment or from an optional file:

```sh
./bin/demonware-server -env server.env
```

Files use `NAME=value`, one entry per line. Values are literal: do not add shell
quotes; spaces are preserved. Existing process variables, including explicitly
empty values, take precedence over the file. Empty settings use their defaults
where defined. Booleans accept `0`/`false` and `1`/`true`.

Invalid values, missing requirements and unknown `CTR_`, `D3_`, `DASH_`,
`NEXTENDO_` or `SHADNET_` settings stop startup. Paths are relative to the process
working directory unless absolute. Filled env files are private and ignored by Git.

## Admission and matchmaking

Admission chooses who may connect; matchmaking chooses where rooms are managed.
These are separate settings.

| `CTR_ADMISSION` | Requirements | Identity and trust |
| --- | --- | --- |
| `shadnet` (default) | All five `SHADNET_*` settings | Verifies NPAuth ID tokens using the configured issuer's public JWKS endpoint |
| `steam` | `CTR_STEAM_HELPER`, `CTR_STEAM_REALM`; empty `SHADNET_*` | Verifies Steam tickets through a separate native helper; this central path uses AppID 480 |
| `local` | `CTR_COMPANION=stdio`, loopback listeners and a compatible parent; empty `SHADNET_*` | Admits the owner supplied over the inherited companion pipe |
| `guest` | `CTR_UNLOCK_ALL=1`; empty `SHADNET_*` | Assigns a new random publisher identity on every authentication |

`CTR_MATCHMAKING=central` is the default. `CTR_MATCHMAKING=steam` selects
provider-backed peer rooms and requires `CTR_ADMISSION=local`. Setting admission
to `steam` alone does not enable peer matchmaking.

### shadNet

Configure `SHADNET_ISSUER`, `SHADNET_CLIENT_ID`, `SHADNET_TITLE_ID`,
`SHADNET_SCOPES` and `SHADNET_JWKS_URL` for your issuer and registered client.
Scopes are space-separated. The issuer owns accounts and private signing keys;
the publisher fetches public verification keys and shares no account database.
The client needs a host with the matching NPAuth integration and routing to this
publisher. [FORK.md](../FORK.md#client-side) records the integration's provenance.

### Steam central admission

Set an absolute path to `CTR_STEAM_HELPER` and a `CTR_STEAM_REALM` of 1–64
letters, digits, underscores or hyphens. `CTR_STEAM_LIBRARY` optionally selects
the operator-supplied Steam runtime library; otherwise the helper loads it from
its own directory. No Steam Web API key is required. The Go server neither
loads that library nor implements Steam authentication itself.

Use a separate database from shadNet, guest or local admission. See
[Verified Steam admission](STEAM_ADMISSION.md) for the helper and client contract.

### Local publisher / P2P

A host integration such as ctrnf-p2p configures a companion provider, which starts
this executable and supplies a private owner handshake. Do not launch a second
server manually for the same integration. Steam calls, endpoint allocation and
opaque peer transport belong to the provider; CTR room rules and publisher data
belong to this server.

Local admission requires literal loopback `BIND_ADDR` and `NEXTENDO_HOST`,
`CTR_COMPANION=stdio`, redacted logs, no PROXY protocol and no central UDP relay.
`CTR_AUTH_HTTP=1` permits HTTP only on direct loopback. `CTR_LOCAL_WEAK_TOKEN=1`
is a default-off compatibility option for the unmodified native host: it accepts
a fabricated token for the pipe owner's account from loopback. It does not prove
a Steam ticket or a secret; only a host adapter that needs it should enable it.

The parent supplies AppID, title, build, realm and owner through the protocol.
The configurable AppID on this path is distinct from central Steam admission's
fixed 480. Normal and unlock-all players can share the same discovery namespace.
Only room messages and race traffic cross peers; progression, inventory,
leaderboards, news and publisher files stay local. See
[Local publisher and P2P boundary](P2P_LOCAL.md) for the complete contract.

### Anonymous guests

Guest admission is explicit and never a fallback after failed verification.
Each authentication receives a fresh identity, so a returning player cannot
recover it. The client requires an account-free integration; setting server
options alone does not bypass the game's platform checks. Guest operation can
still persist statistics and operational data under ephemeral identities.

## Listeners and client requirements

| Setting | Default | Purpose |
| --- | --- | --- |
| `NEXTENDO_HOST` | `127.0.0.1` | IPv4 address advertised in NAT replies |
| `BIND_ADDR` | Empty (all interfaces) | Listener bind address |
| `AUTH_PORT` | `8460` | Authentication and Umbrella |
| `LOBBY_PORTS` | `3074,3075,3076,3077,3078,3079,3080` | Lobby TCP listeners |
| `NAT_PORT` | `3074` | NAT UDP listener |
| `DASH_PORT` | `8093` | Monitoring HTTP listener |
| `CERT_FILE`, `KEY_FILE` | `cert.pem`, `key.pem` | TLS certificate and private key |

Authentication uses HTTPS by default. Provide a certificate the client accepts
and route the client's auth, Umbrella and lobby destinations to your instance.
These client and network settings are outside this repository. HTTP is an
explicit direct-loopback option, never a general TLS replacement.

Authentication replies are **unsigned**, regardless of admission provider. Enable
the [CTR:NF Server Signature Patch](https://github.com/pierluigizagaria/ctrnf-server-signature-patch)
for the supported game version. There is no response signing key or OpenSSL
dependency; TLS certificates serve a different purpose.

The optional inherited UDP relay is disabled by default and is not qualified for
PS4 races. Local P2P mode refuses it. Configure monitoring access with `DASH_TOKEN`
and your deployment's network controls; the sample `change-me` is a placeholder.

## Economy and storage

`DATA_DIR` defaults to `data`; `ctrnf.db` stores economy, ledger, leaderboards and
player names. One process owns a database at a time. It uses WAL with
`synchronous=FULL`; preserve the database and any live WAL state together when
backing it up. Do not delete SQLite sidecars to work around a lock.

`CTR_UNLOCK_ALL=0` is normal progression. New accounts start with
`CTR_START_BALANCE=0`; changing the setting does not reset existing balances.
With `CTR_UNLOCK_ALL=1`, Pit Stop catalog ownership is computed at read time and
known purchases debit nothing. It does not control single-player unlocks owned
by the game save.

- **Central admission (`shadnet`, `steam`, `guest`):** trust and economy policy
  are bound to the database. Separate normal and unlock-all instances need
  separate data directories, listeners and matchmaking pools.
- **Local admission:** one installation's database is shared by accounts and
  both economy modes. Rows use SteamID; the Steam and Lab backends retain
  separate trust bindings. Unlock-all does not persist catalog grants, while
  earned rewards and statistics can persist in both modes. Compatible v1 local
  bindings upgrade in place; unrelated databases are refused.

Local AppID, realm and account changes do not reset progression. Native saves
remain the host's responsibility. The game can cache inventory in its save;
server-side persistence checks do not establish how an equipped item behaves
after switching economy modes.

## News, diagnostics and shutdown

`CTR_NEWS` sets the main-menu ticker; exact-language `CTR_NEWS_<CODE>` values
override it. An empty override falls back to the default. Text is not translated
automatically. Supported codes and length rules are in `example.env`; detailed
client behavior is recorded in [FORK.md](../FORK.md#main-menu-news).

Logs are redacted by default. Raw dumps can contain credentials and player data;
local admission requires redaction. `D3_PUBFILES` still selects optional generic
CTR physical publisher files; legacy `D3_PUBFILES_CONFIG` and `D3_RIFTDATA` are
accepted but inactive. The `pubfiles` command validates configuration and exits
without generating Diablo files.

SIGINT/SIGTERM closes admission, drains admitted work and closes SQLite before
releasing the instance lock. A single 30-second shutdown bound applies; it is
not an idle or session lifetime limit. A committed operation may outlive its
network reply, so exactly-once delivery is not promised.