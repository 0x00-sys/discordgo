package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Flags
var (
	BotToken = flag.String("token", "", "Bot token")
)

const timeout time.Duration = time.Second * 10

var games map[string]time.Time = make(map[string]time.Time)
var gamesMu sync.Mutex

func main() {
	flag.Parse()
	s, _ := discordgo.New("Bot " + *BotToken)
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		fmt.Println("Bot is ready")
	})
	s.AddHandler(messageCreate)
	s.Identify.Intents = discordgo.MakeIntent(discordgo.IntentsAllWithoutPrivileged | discordgo.IntentsMessageContent)

	err := s.Open()
	if err != nil {
		log.Fatalf("Cannot open the session: %v", err)
	}
	defer s.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	log.Println("Graceful shutdown")

}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if strings.Contains(m.Content, "ping") {
		if ch, err := s.State.Channel(m.ChannelID); err != nil || !ch.IsThread() {
			thread, err := s.MessageThreadStartComplex(m.ChannelID, m.ID, &discordgo.ThreadStart{
				Name:                "Pong game with " + m.Author.Username,
				AutoArchiveDuration: 60,
				Invitable:           false,
				RateLimitPerUser:    10,
			})
			if err != nil {
				panic(err)
			}
			_, _ = s.ChannelMessageSend(thread.ID, "pong")
			m.ChannelID = thread.ID
		} else {
			_, _ = s.ChannelMessageSendReply(m.ChannelID, "pong", m.Reference())
		}
		gamesMu.Lock()
		games[m.ChannelID] = time.Now()
		gamesMu.Unlock()
		<-time.After(timeout)
		gamesMu.Lock()
		lastMessage := games[m.ChannelID]
		gamesMu.Unlock()
		if time.Since(lastMessage) >= timeout {
			archived := true
			locked := true
			_, err := s.ChannelEditComplex(m.ChannelID, &discordgo.ChannelEdit{
				Archived: &archived,
				Locked:   &locked,
			})
			if err != nil {
				panic(err)
			}
		}
	}
}
