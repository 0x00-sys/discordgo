<img align="right" alt="DiscordGo logo" src="/docs/img/discordgo.svg" width="200">

## DiscordGo Ping Pong Example

This example demonstrates how to utilize DiscordGo to create a Ping Pong Bot.

This Bot will respond to "ping" with "Pong!" and "pong" with "Ping!".

**Join [Discord Gophers](https://discord.gg/0f1SbxBZjYoCtNPP)
Discord chat channel for support.**

### Setup

Enable **Message Content Intent** on your application's **Bot** page in the
Discord Developer Portal. This example reads text commands in server messages;
without that intent, Discord sends empty message content. Apps subject to
privileged-intent review also need Discord's approval.

See [Discord's message content intent documentation](https://docs.discord.com/developers/events/gateway#message-content-intent).

### Build

This assumes you already have a working Go environment setup and that
DiscordGo is correctly installed on your system.


From within the pingpong example folder, run the below command to compile the
example.

```sh
go build
```

### Usage

This example authenticates with a bot token from your application's **Bot** page
in the [Discord Developer Portal](https://discord.com/developers/applications).
See [Discord's authentication documentation](https://docs.discord.com/developers/reference#authentication) for supported token types.

```
./pingpong --help
Usage of ./pingpong:
  -t string
        Bot Token
```

The below example shows how to start the bot

```sh
./pingpong -t YOUR_BOT_TOKEN
Bot is now running.  Press CTRL-C to exit.
```
