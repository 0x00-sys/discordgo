# DAVE voice encryption

DiscordGo handles DAVE negotiation, Voice Gateway v8 events, websocket resume,
participant routing, and the placement of DAVE frames inside Discord's voice
transport encryption. The MLS group state and media cryptography come from an
application-supplied backend; DiscordGo does not bundle a native `libdave`
dependency.

Configure the backend factory before joining a voice channel:

```go
session.VoiceDAVESessionFactory = func(
	userID string,
	channelID uint64,
	callbacks discordgo.VoiceDAVECallbacks,
) (discordgo.VoiceDAVESession, error) {
	return newDAVEAdapter(userID, channelID, callbacks)
}
```

`newDAVEAdapter` is application code that wraps the chosen DAVE implementation
and implements `VoiceDAVESession`. The adapter should initialize its MLS
session with `userID` and `channelID`, map `SetLocalSSRC` to Opus, translate the
gateway event methods to the backend, and use `VoiceDAVECallbacks` for DAVE
opcodes 23, 26, 28, and 31. If the backend uses caller-provided output buffers,
the adapter should allocate them using that backend's maximum-frame-size APIs
before returning the resulting slice from `EncryptFrame` or `DecryptFrame`.

`Ready` must remain false while an encryption epoch or transition is not ready.
DiscordGo retains the current outbound Opus frame during that interval instead
of sending it unencrypted. Applications can inspect the same state through
`VoiceConnection.DAVEReady`. `Close` must be safe to call more than once and
must stop all backend work before returning.

A nil factory advertises DAVE protocol version 0. Discord may close the voice
connection with code 4017 when end-to-end encryption is required, and
DiscordGo will not reconnect that unsupported connection. A resumable
websocket interruption preserves the backend and replays missed DAVE events;
a full voice reconnect creates a new backend through the factory.

See Discord's [Voice Connections documentation](https://docs.discord.com/developers/topics/voice-connections)
and the [DAVE protocol specification](https://daveprotocol.com/) when writing
an adapter.
