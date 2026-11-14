// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains high level helper functions and easy entry points for the
// entire discordgo package.  These functions are being developed and are very
// experimental at this point.  They will most likely change so please use the
// low level functions if that's a problem.

// Package discordgo provides Discord binding for Go
package discordgo

import (
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// VERSION of DiscordGo, follows Semantic Versioning. (http://semver.org/)
const VERSION = "0.29.0"

// New creates a new Discord session with provided token.
// If the token is for a bot, it must be prefixed with "Bot "
//
//	e.g. "Bot ..."
//
// Or if it is an OAuth2 token, it must be prefixed with "Bearer "
//
//	e.g. "Bearer ..."
func New(token string) (s *Session, err error) {

	// Create an empty Session interface.
	s = &Session{
		State:                              NewState(),
		Ratelimiter:                        NewRatelimiter(),
		StateEnabled:                       true,
		Compress:                           true,
		ShouldReconnectOnError:             true,
		ShouldReconnectVoiceOnSessionError: true,
		ShouldRetryOnRateLimit:             true,
		ShardID:                            0,
		ShardCount:                         1,
		MaxRestRetries:                     3,
		Client:                             &http.Client{Timeout: (20 * time.Second), Transport: defaultRESTRoundTripper()},
		Dialer:                             websocket.DefaultDialer,
		UserAgent:                          "DiscordBot (https://github.com/bwmarrin/discordgo, v" + VERSION + ")",
		reconnectCancel:                    make(chan struct{}),
		sequence:                           new(int64),
		lastHeartbeatAck:                   time.Now(),
	}

	s.LastHeartbeatAck = s.lastHeartbeatAck.UTC()

	// Initialize the Identify Package with defaults
	// These can be modified prior to calling Open()
	s.Identify.Compress = true
	s.Identify.LargeThreshold = 250
	s.Identify.Properties.OS = runtime.GOOS
	s.Identify.Properties.Browser = "DiscordGo v" + VERSION
	s.Identify.Intents = IntentsAllWithoutPrivileged
	s.Identify.Token = token
	s.Token = token

	return
}

// HTTP/2 health check for the default REST client. Discord serves HTTP/2, so
// all REST traffic to a host shares one connection. Without pings, a
// connection that stops answering without closing keeps getting reused and
// every request on it times out. With these values a dead connection is
// closed at most 15s after the last frame arrived, before the 20s client
// timeout, while a busy process that stalls for a few seconds does not drop
// a healthy connection. They are variables so tests can shorten them.
var (
	restHTTP2SendPingTimeout = 10 * time.Second
	restHTTP2PingTimeout     = 5 * time.Second
)

var (
	restTransportOnce sync.Once
	restTransportBase *http.Transport
	restTransport     *http.Transport
)

// defaultRESTRoundTripper returns the transport for a Session that New
// creates. While http.DefaultTransport is still the transport seen by the
// first New, every Session shares one clone of it with HTTP/2 pings enabled,
// so connections stay pooled process-wide. Otherwise it returns nil, which
// makes the client use http.DefaultTransport as before. That keeps HTTP mocks
// and tracing wrappers installed before New working. The transport is used
// directly instead of through a wrapper because net/http adds a timer and a
// goroutine to every request on transports it does not recognize.
func defaultRESTRoundTripper() http.RoundTripper {
	restTransportOnce.Do(func() {
		original, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return
		}
		pinging := original.Clone()
		var config http.HTTP2Config
		if pinging.HTTP2 != nil {
			config = *pinging.HTTP2
		}
		if config.SendPingTimeout == 0 {
			config.SendPingTimeout = restHTTP2SendPingTimeout
		}
		if config.PingTimeout == 0 {
			config.PingTimeout = restHTTP2PingTimeout
		}
		pinging.HTTP2 = &config
		restTransportBase = original
		restTransport = pinging
	})
	if restTransport == nil {
		return nil
	}
	if current, ok := http.DefaultTransport.(*http.Transport); !ok || current != restTransportBase {
		return nil
	}
	return restTransport
}
