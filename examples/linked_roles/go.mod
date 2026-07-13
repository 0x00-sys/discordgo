module github.com/bwmarrin/discordgo/examples/linked_roles

go 1.25.0

replace github.com/bwmarrin/discordgo v0.26.1 => ../../

require (
	github.com/bwmarrin/discordgo v0.26.1
	github.com/joho/godotenv v1.4.0
	golang.org/x/oauth2 v0.27.0
)

require (
	github.com/gorilla/websocket v1.4.2 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
)
