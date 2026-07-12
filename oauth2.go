// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains functions related to Discord OAuth2 endpoints

package discordgo

import (
	"fmt"
	"time"
)

// OAuth2Authorization stores information about the current OAuth2 authorization.
type OAuth2Authorization struct {
	Application *Application `json:"application"`
	Scopes      []string     `json:"scopes"`
	Expires     time.Time    `json:"expires"`
	User        *User        `json:"user,omitempty"`
}

// OAuth2CurrentAuthorization returns information about the current OAuth2 authorization.
// The session must use a bearer token.
func (s *Session) OAuth2CurrentAuthorization(options ...RequestOption) (authorization *OAuth2Authorization, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointOAuth2CurrentAuthorization, nil, EndpointOAuth2CurrentAuthorization, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &authorization); err != nil {
		return nil, err
	}
	if authorization == nil {
		return nil, fmt.Errorf("%w: oauth2 authorization response is null", ErrJSONUnmarshal)
	}
	if authorization.Application == nil {
		return nil, fmt.Errorf("%w: oauth2 authorization response is missing application", ErrJSONUnmarshal)
	}
	if authorization.Scopes == nil {
		return nil, fmt.Errorf("%w: oauth2 authorization response is missing scopes", ErrJSONUnmarshal)
	}
	if authorization.Expires.IsZero() {
		return nil, fmt.Errorf("%w: oauth2 authorization response is missing expiry", ErrJSONUnmarshal)
	}
	return authorization, nil
}

// ------------------------------------------------------------------------------------------------
// Code specific to Discord OAuth2 Applications
// ------------------------------------------------------------------------------------------------

// The MembershipState represents whether the user is in the team or has been invited into it
type MembershipState int

// Constants for the different stages of the MembershipState
const (
	MembershipStateInvited  MembershipState = 1
	MembershipStateAccepted MembershipState = 2
)

// A TeamMember struct stores values for a single Team Member, extending the normal User data - note that the user field is partial
type TeamMember struct {
	User            *User           `json:"user"`
	TeamID          string          `json:"team_id"`
	MembershipState MembershipState `json:"membership_state"`
	Permissions     []string        `json:"permissions"`
	Role            string          `json:"role,omitempty"`
}

// A Team struct stores the members of a Discord Developer Team as well as some metadata about it
type Team struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Icon        string        `json:"icon"`
	OwnerID     string        `json:"owner_user_id"`
	Members     []*TeamMember `json:"members"`
}

// OAuth2Key is a public JSON Web Key used by Discord's OAuth2 service.
type OAuth2Key struct {
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	KeyID     string `json:"kid"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
	Algorithm string `json:"alg"`
}

// OAuth2Keys stores Discord's OAuth2 JSON Web Key Set.
type OAuth2Keys struct {
	Keys []*OAuth2Key `json:"keys"`
}

// OAuth2PublicKeys returns the public keys used by Discord's OAuth2 service.
func (s *Session) OAuth2PublicKeys(options ...RequestOption) (keys *OAuth2Keys, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointOAuth2Keys, nil, EndpointOAuth2Keys, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &keys); err != nil {
		return nil, err
	}
	if keys == nil {
		return nil, fmt.Errorf("%w: OAuth2 public keys response is null", ErrJSONUnmarshal)
	}
	if keys.Keys == nil {
		return nil, fmt.Errorf("%w: OAuth2 public keys response is missing keys", ErrJSONUnmarshal)
	}
	for _, key := range keys.Keys {
		if key == nil {
			return nil, fmt.Errorf("%w: OAuth2 public keys response contains a null key", ErrJSONUnmarshal)
		}
	}
	return keys, nil
}

// Application returns an Application structure of a specific Application
//   appID : The ID of an Application
func (s *Session) Application(appID string) (st *Application, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointOAuth2Application(appID), nil, EndpointOAuth2Application(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// Applications returns all applications for the authenticated user
func (s *Session) Applications() (st []*Application, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointOAuth2Applications, nil, EndpointOAuth2Applications)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ApplicationCreate creates a new Application
//    name : Name of Application / Bot
//    uris : Redirect URIs (Not required)
func (s *Session) ApplicationCreate(ap *Application) (st *Application, err error) {

	data := struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}{ap.Name, ap.Description}

	body, err := s.RequestWithBucketID("POST", EndpointOAuth2Applications, data, EndpointOAuth2Applications)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ApplicationUpdate updates an existing Application
//   var : desc
func (s *Session) ApplicationUpdate(appID string, ap *Application) (st *Application, err error) {

	data := struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}{ap.Name, ap.Description}

	body, err := s.RequestWithBucketID("PUT", EndpointOAuth2Application(appID), data, EndpointOAuth2Application(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ApplicationDelete deletes an existing Application
//   appID : The ID of an Application
func (s *Session) ApplicationDelete(appID string) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointOAuth2Application(appID), nil, EndpointOAuth2Application(""))
	if err != nil {
		return
	}

	return
}

// Asset struct stores values for an asset of an application
type Asset struct {
	Type int    `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ApplicationAssets returns an application's assets
func (s *Session) ApplicationAssets(appID string) (ass []*Asset, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointOAuth2ApplicationAssets(appID), nil, EndpointOAuth2ApplicationAssets(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &ass)
	return
}

// ------------------------------------------------------------------------------------------------
// Code specific to Discord OAuth2 Application Bots
// ------------------------------------------------------------------------------------------------

// ApplicationBotCreate creates an Application Bot Account
//
//   appID : The ID of an Application
//
// NOTE: func name may change, if I can think up something better.
func (s *Session) ApplicationBotCreate(appID string) (st *User, err error) {

	body, err := s.RequestWithBucketID("POST", EndpointOAuth2ApplicationsBot(appID), nil, EndpointOAuth2ApplicationsBot(""))
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}
