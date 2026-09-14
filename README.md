# DiscordGo (maintained fork)

<img align="right" alt="DiscordGo logo" src="docs/img/discordgo.svg" width="400">

This is an actively maintained fork of
[bwmarrin/discordgo](https://github.com/bwmarrin/discordgo), the
[Go](https://go.dev/) bindings for the [Discord](https://discord.com/) API.

As of September 2026, upstream has not merged a change since February 2026 or
tagged a release since v0.29.0 (May 2025). Discord has kept changing in the
meantime, most visibly by requiring DAVE end-to-end encryption for voice since
March 1, 2026. This fork keeps the same import path and package API, so existing
bots can switch with one `replace` line, and continues development on top of
the full upstream history.

This fork is not affiliated with or endorsed by the original DiscordGo authors.

## The fork at a glance

Since the fork started in May 2026:

- **320 commits** across **284 merged pull requests**
- **About 200 bug fixes** in the gateway, voice, state cache and REST client
- **79 new API methods** (232 → 311 on `Session`), **12 new gateway events**
  and **135 new exported types**
- **About 30,000 lines of new tests**, run with the race detector
- **Discord API v10 by default** (upstream still defaults to v9)

## Features upstream doesn't have

- **DAVE voice encryption.** Discord has required DAVE for voice since March 1,
  2026, and upstream has no DAVE code. This fork handles DAVE negotiation,
  Voice Gateway v8 events, resume, participant routing and frame placement. You
  plug in the MLS and media crypto backend (for example a `libdave` binding),
  so there is no CGO dependency. See [docs/DAVE.md](docs/DAVE.md).
- **Lobbies.** Create, join, edit and delete lobbies, manage members, send
  messages and link channels (16 methods).
- **Soundboard.** Guild soundboard sound management, default sounds, playing
  sounds in voice, soundboard gateway events and state tracking.
- **Guild management.** Join requests, bulk bans, sticker management, welcome
  screen, vanity URL, widget, home settings, incident actions and editing the
  bot's own member.
- **Search.** Guild message search and thread search.
- **Recurring scheduled events.** Recurrence rules, per-occurrence exceptions
  and user counts.
- **Applications and OAuth2.** Fetch and edit applications, upload application
  attachments, current authorization, OpenID user info, public keys,
  entitlements and role connection deletion.
- **Voice.** Voice channel status, editing voice states over REST, and voice
  channel effect and start time events.
- **Newer API fields.** Components v2 media fields, file upload type filters,
  interaction callback responses, super reactions, avatar decorations,
  nameplates, invite target users, rich presence fields and a `RATE_LIMITED`
  gateway event.

## Reliability fixes

- **Gateway.** Heartbeat jitter and monotonic heartbeat timing,
  reconnect after a missed heartbeat ACK, write deadlines, recovery from
  malformed invalid-session payloads and safer reconnect locking.
- **Voice.** Recovery from heartbeat, UDP write and UDP keepalive
  failures, clean shutdown of reconnects and listeners, and fewer allocations
  per RTP packet.
- **State cache.** Fixes for races, stale indexes and nil or malformed events,
  plus tracking of scheduled events, soundboard sounds, stage instances and
  reactions, with less copying on updates.
- **REST and rate limits.** Global rate limit handling, `Retry-After` fallbacks,
  non-JSON error responses and canceled waiters that no longer leak.
- **Safer debug logging.** Bot tokens, webhook and interaction tokens are
  redacted from REST and gateway debug output.

## Getting Started

### Installing

Requires Go 1.25 or newer.

The fork keeps the `github.com/bwmarrin/discordgo` module path, so you install
it with a `replace` directive and your imports stay the same:

```sh
go get github.com/bwmarrin/discordgo
go mod edit -replace github.com/bwmarrin/discordgo=github.com/0x00-sys/discordgo@master
go mod tidy
```

`go mod tidy` pins `@master` to the current commit in your `go.mod`. Run the
same commands again to update. `go get github.com/0x00-sys/discordgo` does not
work on its own, because the module declares the upstream path.

`replace` directives only apply to the main module. If you publish a library
that depends on this fork, applications using that library need to add the
same `replace` line.

### Usage

Import the package into your project.

```go
import "github.com/bwmarrin/discordgo"
```

Construct a new Discord client which can be used to access the variety of
Discord API functions and to set callback functions for Discord events.

```go
discord, err := discordgo.New("Bot " + "authentication token")
```

See [examples](examples) for complete programs.

### Switching from upstream

Most bots build unchanged. Things to check:

- The default API version is 10 (upstream uses 9).
- The session reconnects after one missed heartbeat ACK instead of waiting
  five heartbeat intervals.
- `IntentsAll`, `IntentsAllWithoutPrivileged` and the `PermissionAll*`
  constants include Discord's newer values. `SubscriptionStatusInactive` and
  `SubscriptionStatusEnding` now match Discord's documented values.
- `GuildScheduledEvent`, `Invite`, `MessageAttachment`, `MessageReactions` and
  `MessageUpdate` gained new fields and can no longer be compared with `==`.
- `PollAnswerVoters` and `PollExpire` accept optional `RequestOption`s. Existing
  calls compile as before.
- Voice channels that require end-to-end encryption need a
  `VoiceDAVESessionFactory`. Without one, Discord can close the connection with
  code 4017. See [docs/DAVE.md](docs/DAVE.md).

## Documentation

**NOTICE**: This library and the Discord API are unfinished.
Because of that there may be major changes to library in the future.

The code is documented inline. pkg.go.dev only shows the upstream module, so
read this fork's documentation with `go doc` from a project that uses the
`replace` above, for example `go doc github.com/bwmarrin/discordgo Session`.

- [DAVE voice encryption](docs/DAVE.md)
- [Getting started](docs/GettingStarted.md)
- [Examples](examples)
- The upstream wiki's [Troubleshooting](https://github.com/bwmarrin/discordgo/wiki/Troubleshooting)
  page and [Awesome DiscordGo](https://github.com/bwmarrin/discordgo/wiki/Awesome-DiscordGo)
  list still mostly apply.

## Issues and Contributing

Report bugs in this fork on [this repository's issues](https://github.com/0x00-sys/discordgo/issues),
not upstream.

Contributions are very welcome. Please follow the guidelines below and in
[CONTRIBUTING.md](CONTRIBUTING.md).

- Open an issue describing the bug or enhancement first, so it can be discussed.
- Match current naming conventions as closely as possible.
- This package is intended to be a low level direct mapping of the Discord API,
  so please avoid adding enhancements outside of that scope without first
  discussing it.
- Run `gofmt`, `go vet ./...` and `go test -race ./...` before opening a Pull
  Request against the master branch.

## Credits

DiscordGo was created by [Bruce Marriner](https://github.com/bwmarrin) and is
built on years of work by the [upstream contributors](https://github.com/bwmarrin/discordgo/graphs/contributors).
Their full commit history is preserved in this repository.

[Chris Rhodes](https://github.com/iopred) - For the DiscordGo logo and tons of PRs.

## License

BSD 3-Clause, the same license as upstream. The original copyright notice,
conditions and disclaimer are kept unchanged in [LICENSE](LICENSE), and changes
in this fork are distributed under the same terms.
