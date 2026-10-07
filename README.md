# upmiigo-bridge

A Matrix bridge for [up mii go](https://upmiigo.co.uk) direct messages, built on
[mautrix-go](https://github.com/mautrix/go)'s bridgev2. Run it with Beeper's bridge manager to get your
up mii go chats in [Beeper](https://www.beeper.com), next to everything else.

**What's bridged:** one-to-one messages both ways, photos (with captions) both ways, read receipts both
ways ("Seen" on up mii go), member names, avatars and verified ticks. The last 30 messages of each
conversation come in when you first log in, and new ones arrive within a second or two.

Blocks, suspensions and rate limits apply exactly as they do on the site.

## Set up with Beeper

1. Make a token on up mii go: **Settings → Beeper bridge → Make a token**. Copy it; it's only shown once.
2. Install [bbctl](https://github.com/beeper/bridge-manager) and log in: `bbctl login`
3. Build the bridge (needs Go 1.24+): `./build.sh`
4. Have bbctl write a config for it: `bbctl config --type bridgev2 -o config.yaml sh-upmiigo`
5. Run it, and leave it running: `./upmiigo-bridge -c config.yaml`
6. In Beeper, open the chat with the bridge bot, send `login`, and paste your token.

To stop bridging, send `logout` to the bot and revoke the token on up mii go.

## Config

The `network` section of `config.yaml`:

```yaml
network:
    # The up mii go site to bridge.
    server_url: https://upmiigo.co.uk
    # Recent messages to bring in per conversation on first login (max 100).
    initial_messages: 30
```

## How it works

The bridge talks to the site's bridge API (`/api/bridge/v1`) with your token:

| Endpoint | Used for |
|---|---|
| `GET /me` | checking the token at login |
| `GET /conversations`, `GET /conversations/:id` | the chat list and who each chat is with |
| `GET /conversations/:id/messages` | first-login history |
| `GET /events?since=…&wait=25` | long-polling for new messages, chats and read receipts |
| `POST /conversations/:id/messages` | sending text |
| `POST /conversations/:id/photos` | sending photos (multipart `file`, optional `caption`) |
| `POST /conversations/:id/read` | read receipts |
| `GET /users/:id` | names and avatars |

## Development

```sh
go vet -tags goolm ./...
# Read-only checks against a running site (needs a real token):
UPMIIGO_URL=http://localhost:3000 UPMIIGO_TOKEN=umgb_... go test ./pkg/upmiigo -run Live -v
```
