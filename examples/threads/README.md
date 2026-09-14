<img align="right" alt="DiscordGo logo" src="/docs/img/discordgo.svg" width="200">

## DiscordGo Threads Example

This example demonstrates how to utilize DiscordGo to manage channel threads.

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

From within the threads example folder, run the below command to compile the
example.

```sh
go build
```

### Usage

```
Usage of threads:
  -token string
    	Bot token
```

The below example shows how to start the bot from the threads example folder.

```sh
./threads -token YOUR_BOT_TOKEN
```
