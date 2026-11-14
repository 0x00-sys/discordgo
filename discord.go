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
	restTransport     http.RoundTripper
)

// defaultRESTRoundTripper returns the transport shared by every Session that
// New creates, so connections stay pooled process-wide. It returns nil, which
// makes the client use http.DefaultTransport as before, when
// http.DefaultTransport is not an *http.Transport.
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
		restTransport = &restRoundTripper{original: original, pinging: pinging}
	})
	return restTransport
}

// restRoundTripper sends requests over a clone of http.DefaultTransport with
// HTTP/2 pings enabled. If the application later replaces
// http.DefaultTransport (HTTP mocks, tracing wrappers), requests go to the
// replacement instead, as they did when the client had no transport set.
type restRoundTripper struct {
	original *http.Transport
	pinging  *http.Transport
}

func (t *restRoundTripper) current() http.RoundTripper {
	if current, ok := http.DefaultTransport.(*http.Transport); ok && current == t.original {
		return t.pinging
	}
	return http.DefaultTransport
}

// RoundTrip implements http.RoundTripper.
func (t *restRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.current().RoundTrip(req)
}

// CloseIdleConnections closes idle connections of the transport in use, so
// Session.Client.CloseIdleConnections keeps working.
func (t *restRoundTripper) CloseIdleConnections() {
	t.pinging.CloseIdleConnections()
	if current, ok := http.DefaultTransport.(*http.Transport); ok && current == t.original {
		return
	}
	if closer, ok := http.DefaultTransport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
