// Discordgo - Discord bindings for Go
// Available at https://github.com/bwmarrin/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains functions for interacting with the Discord REST/JSON API
// at the lowest level.

package discordgo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // For JPEG decoding
	_ "image/png"  // For PNG decoding
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"context"
)

// All error constants
var (
	ErrJSONUnmarshal           = errors.New("json unmarshal")
	ErrStatusOffline           = errors.New("You can't set your Status to offline")
	ErrVerificationLevelBounds = errors.New("VerificationLevel out of bounds, should be between 0 and 3")
	ErrPruneDaysBounds         = errors.New("the number of days should be more than or equal to 1")
	ErrGuildNoIcon             = errors.New("guild does not have an icon set")
	ErrGuildNoSplash           = errors.New("guild does not have a splash set")
	ErrUnauthorized            = errors.New("HTTP request was unauthorized. This could be because the provided token was not a bot token. Please add \"Bot \" to the start of your token. https://discord.com/developers/docs/reference#authentication-example-bot-token-authorization-header")
)

var (
	// Marshal defines function used to encode JSON payloads
	Marshal func(v interface{}) ([]byte, error) = json.Marshal
	// Unmarshal defines function used to decode JSON payloads
	Unmarshal func(src []byte, v interface{}) error = json.Unmarshal
)

// RESTError stores error information about a request with a bad response code.
// Message is not always present, there are cases where api calls can fail
// without returning a json message.
type RESTError struct {
	Request      *http.Request
	Response     *http.Response
	ResponseBody []byte

	Message *APIErrorMessage // Message may be nil.
}

// ThreadSearchIndexNotReadyError is returned when Discord is still indexing
// the channel for thread search.
type ThreadSearchIndexNotReadyError struct {
	RESTError        *RESTError `json:"-"`
	Message          string     `json:"message"`
	Code             int        `json:"code"`
	DocumentsIndexed int        `json:"documents_indexed"`
	RetryAfter       int        `json:"retry_after"`
}

// Error returns the underlying REST error when available.
func (e *ThreadSearchIndexNotReadyError) Error() string {
	if e.RESTError != nil {
		return e.RESTError.Error()
	}
	return e.Message
}

// Unwrap returns the underlying REST error.
func (e *ThreadSearchIndexNotReadyError) Unwrap() error {
	return e.RESTError
}

// newRestError returns a new REST API error.
func newRestError(req *http.Request, resp *http.Response, body []byte) *RESTError {
	req = redactedRequest(req)
	resp = redactedResponse(resp, req)

	restErr := &RESTError{
		Request:      req,
		Response:     resp,
		ResponseBody: body,
	}

	// Attempt to decode the error and assume no message was provided if it fails
	var msg *APIErrorMessage
	err := Unmarshal(body, &msg)
	if err == nil {
		restErr.Message = msg
	}

	return restErr
}

func redactedRequest(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}

	redacted := req.Clone(req.Context())
	redacted.Body = nil
	redacted.GetBody = nil
	if redacted.Header.Get("Authorization") != "" {
		redacted.Header.Set("Authorization", redactedValue)
	}
	if redacted.URL != nil {
		if u, err := url.Parse(redactedURL(redacted.URL.String())); err == nil {
			redacted.URL = u
		}
	}
	return redacted
}

func redactedResponse(resp *http.Response, req *http.Request) *http.Response {
	if resp == nil {
		return nil
	}

	redacted := new(http.Response)
	*redacted = *resp
	redacted.Body = http.NoBody
	redacted.Request = req
	return redacted
}

// Error returns a Rest API Error with its status code and body.
func (r RESTError) Error() string {
	return "HTTP " + r.Response.Status + ", " + string(r.ResponseBody)
}

// RateLimitError is returned when a request exceeds a rate limit
// and ShouldRetryOnRateLimit is false. The request may be manually
// retried after waiting the duration specified by RetryAfter.
type RateLimitError struct {
	*RateLimit
}

// Error returns a rate limit error with rate limited endpoint and retry time.
func (e RateLimitError) Error() string {
	return "Rate limit exceeded on " + redactedURL(e.URL) + ", retry after " + e.RetryAfter.String()
}

// RequestConfig is an HTTP request configuration.
type RequestConfig struct {
	Request                *http.Request
	ShouldRetryOnRateLimit bool
	MaxRestRetries         int
	Client                 *http.Client
}

// newRequestConfig returns a new HTTP request configuration based on parameters in Session.
func newRequestConfig(s *Session, req *http.Request) *RequestConfig {
	return &RequestConfig{
		ShouldRetryOnRateLimit: s.ShouldRetryOnRateLimit,
		MaxRestRetries:         s.MaxRestRetries,
		Client:                 s.Client,
		Request:                req,
	}
}

// RequestOption is a function which mutates request configuration.
// It can be supplied as an argument to any REST method.
type RequestOption func(cfg *RequestConfig)

// WithClient changes the HTTP client used for the request.
func WithClient(client *http.Client) RequestOption {
	return func(cfg *RequestConfig) {
		if client != nil {
			cfg.Client = client
		}
	}
}

// WithRetryOnRatelimit controls whether session will retry the request on rate limit.
func WithRetryOnRatelimit(retry bool) RequestOption {
	return func(cfg *RequestConfig) {
		cfg.ShouldRetryOnRateLimit = retry
	}
}

// WithRestRetries changes maximum amount of retries if request fails.
func WithRestRetries(max int) RequestOption {
	return func(cfg *RequestConfig) {
		cfg.MaxRestRetries = max
	}
}

// WithHeader sets a header in the request.
func WithHeader(key, value string) RequestOption {
	return func(cfg *RequestConfig) {
		cfg.Request.Header.Set(key, value)
	}
}

func withoutAuthorization() RequestOption {
	return func(cfg *RequestConfig) {
		cfg.Request.Header.Del("Authorization")
	}
}

func withoutAuthorizationOptions(options []RequestOption) []RequestOption {
	unauthenticated := make([]RequestOption, 0, len(options)+1)
	unauthenticated = append(unauthenticated, options...)
	unauthenticated = append(unauthenticated, withoutAuthorization())
	return unauthenticated
}

// WithAuditLogReason changes audit log reason associated with the request.
func WithAuditLogReason(reason string) RequestOption {
	return WithHeader("X-Audit-Log-Reason", reason)
}

// WithLocale changes accepted locale of the request.
func WithLocale(locale Locale) RequestOption {
	return WithHeader("X-Discord-Locale", string(locale))
}

// WithContext changes context of the request.
func WithContext(ctx context.Context) RequestOption {
	return func(cfg *RequestConfig) {
		cfg.Request = cfg.Request.WithContext(ctx)
	}
}

// Request is the same as RequestWithBucketID but the bucket id is the same as the urlStr
func (s *Session) Request(method, urlStr string, data interface{}, options ...RequestOption) (response []byte, err error) {
	return s.RequestWithBucketID(method, urlStr, data, strings.SplitN(urlStr, "?", 2)[0], options...)
}

// RequestWithBucketID makes a (GET/POST/...) Requests to Discord REST API with JSON data.
func (s *Session) RequestWithBucketID(method, urlStr string, data interface{}, bucketID string, options ...RequestOption) (response []byte, err error) {
	var body []byte
	if data != nil {
		body, err = Marshal(data)
		if err != nil {
			return
		}
	}

	return s.RequestRaw(method, urlStr, "application/json", body, bucketID, 0, options...)
}

func (s *Session) requestWithEphemeralBucketID(method, urlStr string, data interface{}, bucketID string, useGlobalRateLimit bool, options ...RequestOption) (response []byte, err error) {
	var body []byte
	if data != nil {
		body, err = Marshal(data)
		if err != nil {
			return
		}
	}

	return s.requestRawWithEphemeralBucket(method, urlStr, "application/json", body, bucketID, 0, useGlobalRateLimit, options...)
}

// RequestRaw makes a (GET/POST/...) Requests to Discord REST API.
// Preferably use the other Request* methods but this lets you send JSON directly if that's what you have.
// Sequence is the sequence number. If a retry-safe request fails with a transient
// 5xx response, it will retry with sequence+1 until it either succeeds or
// sequence >= session.MaxRestRetries.
func (s *Session) RequestRaw(method, urlStr, contentType string, b []byte, bucketID string, sequence int, options ...RequestOption) (response []byte, err error) {
	if bucketID == "" {
		bucketID = strings.SplitN(urlStr, "?", 2)[0]
	}

	cfg, err := s.requestConfig(method, urlStr, contentType, b, options...)
	if err != nil {
		return
	}

	bucket, err := s.Ratelimiter.LockBucketContext(cfg.Request.Context(), bucketID)
	if err != nil {
		return
	}

	return s.requestWithLockedBucket(method, urlStr, contentType, b, bucket, sequence, cfg, options...)
}

func (s *Session) requestRawWithEphemeralBucket(method, urlStr, contentType string, b []byte, bucketID string, sequence int, useGlobalRateLimit bool, options ...RequestOption) (response []byte, err error) {
	if bucketID == "" {
		bucketID = strings.SplitN(urlStr, "?", 2)[0]
	}

	cfg, err := s.requestConfig(method, urlStr, contentType, b, options...)
	if err != nil {
		return
	}

	bucket, err := s.Ratelimiter.lockEphemeralBucketContext(cfg.Request.Context(), bucketID, useGlobalRateLimit)
	if err != nil {
		return
	}

	return s.requestWithLockedBucket(method, urlStr, contentType, b, bucket, sequence, cfg, options...)
}

func (s *Session) requestWithBucketIDNoGlobal(method, urlStr string, data interface{}, bucketID string, options ...RequestOption) (response []byte, err error) {
	return s.requestWithBucketIDNoGlobalTTL(method, urlStr, data, bucketID, 0, options...)
}

func (s *Session) requestWithBucketIDNoGlobalTTL(method, urlStr string, data interface{}, bucketID string, bucketTTL time.Duration, options ...RequestOption) (response []byte, err error) {
	var body []byte
	if data != nil {
		body, err = Marshal(data)
		if err != nil {
			return
		}
	}

	return s.requestRawWithBucketIDNoGlobalTTL(method, urlStr, "application/json", body, bucketID, 0, bucketTTL, options...)
}

func (s *Session) requestRawWithBucketIDNoGlobal(method, urlStr, contentType string, b []byte, bucketID string, sequence int, options ...RequestOption) (response []byte, err error) {
	return s.requestRawWithBucketIDNoGlobalTTL(method, urlStr, contentType, b, bucketID, sequence, 0, options...)
}

func (s *Session) requestRawWithBucketIDNoGlobalTTL(method, urlStr, contentType string, b []byte, bucketID string, sequence int, bucketTTL time.Duration, options ...RequestOption) (response []byte, err error) {
	if bucketID == "" {
		bucketID = strings.SplitN(urlStr, "?", 2)[0]
	}

	cfg, err := s.requestConfig(method, urlStr, contentType, b, options...)
	if err != nil {
		return
	}

	bucket, err := s.Ratelimiter.lockBucketContextWithTTL(cfg.Request.Context(), bucketID, false, bucketTTL)
	if err != nil {
		return
	}

	return s.requestWithLockedBucket(method, urlStr, contentType, b, bucket, sequence, cfg, options...)
}

// RequestWithLockedBucket makes a request using a bucket that's already been locked
func (s *Session) RequestWithLockedBucket(method, urlStr, contentType string, b []byte, bucket *Bucket, sequence int, options ...RequestOption) (response []byte, err error) {
	cfg, err := s.requestConfig(method, urlStr, contentType, b, options...)
	if err != nil {
		bucket.Release(nil)
		return
	}

	return s.requestWithLockedBucket(method, urlStr, contentType, b, bucket, sequence, cfg, options...)
}

func (s *Session) requestConfig(method, urlStr, contentType string, b []byte, options ...RequestOption) (*RequestConfig, error) {
	req, err := http.NewRequest(method, urlStr, bytes.NewBuffer(b))
	if err != nil {
		return nil, redactedRequestError(err)
	}

	// Not used on initial login..
	// TODO: Verify if a login, otherwise complain about no-token
	if s.Token != "" {
		req.Header.Set("authorization", s.Token)
	}

	// Discord's API returns a 400 Bad Request is Content-Type is set, but the
	// request body is empty.
	if b != nil {
		req.Header.Set("Content-Type", contentType)
	}

	// TODO: Make a configurable static variable.
	req.Header.Set("User-Agent", s.UserAgent)

	cfg := newRequestConfig(s, req)
	for _, opt := range options {
		opt(cfg)
	}

	return cfg, nil
}

type requestRetry struct {
	bucket   *Bucket
	sequence int
}

func (s *Session) requestWithLockedBucket(method, urlStr, contentType string, b []byte, bucket *Bucket, sequence int, cfg *RequestConfig, options ...RequestOption) (response []byte, err error) {
	for {
		var retry requestRetry
		response, retry, err = s.requestWithLockedBucketAttempt(method, urlStr, contentType, b, bucket, sequence, cfg)
		if err != nil || retry.bucket == nil {
			return
		}

		bucket = retry.bucket
		sequence = retry.sequence
		cfg, err = s.requestConfig(method, urlStr, contentType, b, options...)
		if err != nil {
			_ = bucket.Release(nil)
			response = nil
			return
		}
	}
}

func (s *Session) requestWithLockedBucketAttempt(method, urlStr, contentType string, b []byte, bucket *Bucket, sequence int, cfg *RequestConfig) (response []byte, retry requestRetry, err error) {
	if s.Debug {
		log.Printf("API REQUEST %8s :: %s\n", method, redactedURL(urlStr))
		log.Printf("API REQUEST  PAYLOAD :: [%s]\n", redactedRESTBody(b, contentType))
	}

	req := cfg.Request

	if s.Debug {
		for k, v := range req.Header {
			log.Printf("API REQUEST   HEADER :: [%s] = %+v\n", k, redactedHeaderValues(k, v))
		}
	}

	resp, err := cfg.Client.Do(req)
	if err != nil {
		bucket.Release(nil)
		err = redactedRequestError(err)
		return
	}
	respBodyClosed := false
	closeResponseBody := func() {
		if respBodyClosed {
			return
		}
		err2 := resp.Body.Close()
		respBodyClosed = true
		if s.Debug && err2 != nil {
			log.Println("error closing resp body")
		}
	}
	defer closeResponseBody()

	err = bucket.Release(resp.Header)
	if err != nil {
		return
	}

	response, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	if s.Debug {

		log.Printf("API RESPONSE  STATUS :: %s\n", resp.Status)
		for k, v := range resp.Header {
			log.Printf("API RESPONSE  HEADER :: [%s] = %+v\n", k, v)
		}
		log.Printf("API RESPONSE    BODY :: [%s]\n\n\n", redactedRESTBody(response, resp.Header.Get("Content-Type")))
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusCreated:
	case http.StatusNoContent:
	case http.StatusInternalServerError:
		fallthrough
	case http.StatusServiceUnavailable:
		fallthrough
	case http.StatusGatewayTimeout:
		fallthrough
	case http.StatusBadGateway:
		// Retry sending request if possible. Avoid replaying non-idempotent
		// requests because Discord may have already committed the write.
		if !shouldRetryRESTRequest(method) {
			err = newRestError(req, resp, response)
		} else if sequence < cfg.MaxRestRetries {

			s.log(LogInformational, "%s Failed (%s), Retrying...", redactedURL(urlStr), resp.Status)
			closeResponseBody()
			bucket, err = s.Ratelimiter.LockBucketObjectContext(req.Context(), bucket)
			if err != nil {
				return
			}
			retry = requestRetry{bucket: bucket, sequence: sequence + 1}
		} else {
			err = fmt.Errorf("Exceeded Max retries HTTP %s, %s", resp.Status, response)
		}
	case http.StatusTooManyRequests:
		rl := TooManyRequests{}
		err = Unmarshal(response, &rl)
		if err != nil {
			retryAfter, parseErr := retryAfterHeader(resp.Header)
			if parseErr != nil {
				s.log(LogError, "rate limit unmarshal error, %s", err)
				return
			}
			err = nil
			rl.Message = strings.TrimSpace(string(response))
			rl.RetryAfter = retryAfter
		}
		if rl.RetryAfter <= 0 {
			if retryAfter, parseErr := retryAfterHeader(resp.Header); parseErr == nil && retryAfter > 0 {
				rl.RetryAfter = retryAfter
			}
		}
		if rl.Global || strings.EqualFold(resp.Header.Get("X-RateLimit-Global"), "true") || strings.EqualFold(resp.Header.Get("X-RateLimit-Scope"), "global") {
			bucket.setGlobalReset(time.Now().Add(rl.RetryAfter))
		}

		rateLimit := &RateLimit{TooManyRequests: &rl, URL: redactedURL(urlStr)}
		if cfg.ShouldRetryOnRateLimit {
			if rl.RetryAfter <= 0 {
				err = &RateLimitError{rateLimit}
				return
			}
			url := rateLimit.URL
			s.log(LogInformational, "Rate Limiting %s, retry in %v", url, rl.RetryAfter)
			s.handleEvent(rateLimitEventType, rateLimit)

			closeResponseBody()
			err = sleepWithContext(req.Context(), rl.RetryAfter)
			if err != nil {
				return
			}
			// we can make the above smarter
			// this method can cause longer delays than required

			bucket, err = s.Ratelimiter.LockBucketObjectContext(req.Context(), bucket)
			if err != nil {
				return
			}
			retry = requestRetry{bucket: bucket, sequence: sequence}
		} else {
			err = &RateLimitError{rateLimit}
		}
	case http.StatusUnauthorized:
		if strings.Index(s.Token, "Bot ") != 0 && strings.Index(s.Token, "Bearer ") != 0 {
			s.log(LogInformational, "%s", ErrUnauthorized)
			err = ErrUnauthorized
			break
		}
		fallthrough
	default: // Error condition
		err = newRestError(req, resp, response)
	}

	return
}

func shouldRetryRESTRequest(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func retryAfterHeader(headers http.Header) (time.Duration, error) {
	retryAfter := headers.Get("Retry-After")
	if retryAfter == "" {
		return 0, errors.New("Retry-After header not found")
	}

	seconds, err := strconv.ParseFloat(retryAfter, 64)
	if err != nil {
		return 0, err
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const (
	redactedValue         = "[REDACTED]"
	redactedURLValue      = "REDACTED"
	redactedMultipartBody = "[MULTIPART BODY REDACTED]"
)

const interactionWebhookBucketTTL = 20 * time.Minute

func redactedHeaderValues(key string, values []string) []string {
	if strings.EqualFold(key, "Authorization") {
		return []string{redactedValue}
	}

	return values
}

func redactedURL(rawurl string) string {
	u, err := url.Parse(rawurl)
	if err != nil {
		return redactedURLString(rawurl)
	}

	parts := strings.Split(u.Path, "/")
	for i, part := range parts {
		if (part == "webhooks" || part == "interactions") && i+2 < len(parts) {
			parts[i+2] = redactedURLValue
		}
	}
	u.Path = strings.Join(parts, "/")

	return u.String()
}

func redactedURLString(rawurl string) string {
	for _, marker := range []string{"/webhooks/", "/interactions/"} {
		rawurl = redactedURLStringSegment(rawurl, marker)
	}
	return rawurl
}

func redactedURLStringSegment(rawurl, marker string) string {
	searchStart := 0
	for {
		markerIndex := strings.Index(rawurl[searchStart:], marker)
		if markerIndex == -1 {
			return rawurl
		}

		idStart := searchStart + markerIndex + len(marker)
		idEndOffset := strings.Index(rawurl[idStart:], "/")
		if idEndOffset == -1 {
			return rawurl
		}

		tokenStart := idStart + idEndOffset + 1
		tokenEnd := len(rawurl)
		if tokenEndOffset := strings.IndexAny(rawurl[tokenStart:], "/?#"); tokenEndOffset != -1 {
			tokenEnd = tokenStart + tokenEndOffset
		}
		if tokenEnd <= tokenStart {
			searchStart = tokenStart
			continue
		}

		rawurl = rawurl[:tokenStart] + redactedURLValue + rawurl[tokenEnd:]
		searchStart = tokenStart + len(redactedURLValue)
	}
}

func redactedRequestError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}

	redacted := *urlErr
	redacted.URL = redactedURL(urlErr.URL)
	return &redacted
}

func redactedRESTBody(body []byte, contentTypes ...string) string {
	if len(body) == 0 {
		return string(body)
	}

	for _, contentType := range contentTypes {
		contentType = strings.ToLower(contentType)
		if strings.HasPrefix(contentType, "multipart/form-data") {
			return redactedMultipartBody
		}
		if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
			return redactedFormBody(body)
		}
	}

	var v interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		return string(body)
	}

	redactRESTValue(v)

	b, err := json.Marshal(v)
	if err != nil {
		return string(body)
	}

	return string(b)
}

func redactedFormBody(body []byte) string {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return string(body)
	}

	for key := range values {
		if !isRESTSecretKey(key) {
			continue
		}
		for i := range values[key] {
			values[key][i] = redactedValue
		}
	}

	return values.Encode()
}

func redactRESTValue(v interface{}) {
	switch value := v.(type) {
	case map[string]interface{}:
		for k, nested := range value {
			if isRESTSecretKey(k) {
				value[k] = redactedValue
				continue
			}
			redactRESTValue(nested)
		}
	case []interface{}:
		for _, nested := range value {
			redactRESTValue(nested)
		}
	}
}

func isRESTSecretKey(key string) bool {
	switch key {
	case "token", "access_token", "refresh_token", "client_secret", "password":
		return true
	}

	return false
}

func unmarshal(data []byte, v interface{}) error {
	err := Unmarshal(data, v)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrJSONUnmarshal, err)
	}

	return nil
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Users
// ------------------------------------------------------------------------------------------------

// User returns the user details of the given userID
// userID    : A user ID or "@me" which is a shortcut of current user ID
func (s *Session) User(userID string, options ...RequestOption) (st *User, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointUser(userID), nil, EndpointUsers, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserAvatar is deprecated. Please use UserAvatarDecode
// userID    : A user ID or "@me" which is a shortcut of current user ID
func (s *Session) UserAvatar(userID string, options ...RequestOption) (img image.Image, err error) {
	u, err := s.User(userID, options...)
	if err != nil {
		return
	}
	img, err = s.UserAvatarDecode(u, options...)
	return
}

// UserAvatarDecode returns an image.Image of a user's Avatar
// user : The user which avatar should be retrieved
func (s *Session) UserAvatarDecode(u *User, options ...RequestOption) (img image.Image, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointUserAvatar(u.ID, u.Avatar), nil, EndpointUserAvatar("", ""), options...)
	if err != nil {
		return
	}

	img, _, err = image.Decode(bytes.NewReader(body))
	return
}

// UserUpdate updates non-empty current user settings.
// Use UserUpdateComplex to explicitly clear the avatar or banner.
func (s *Session) UserUpdate(username, avatar, banner string, options ...RequestOption) (st *User, err error) {
	data := &UserUpdateParams{}
	if username != "" {
		data.Username = &username
	}
	if avatar != "" {
		data.Avatar = &avatar
	}
	if banner != "" {
		data.Banner = &banner
	}
	return s.UserUpdateComplex(data, options...)
}

// UserUpdateComplex updates current user settings, including nullable avatar and banner fields.
func (s *Session) UserUpdateComplex(data *UserUpdateParams, options ...RequestOption) (st *User, err error) {

	body, err := s.RequestWithBucketID("PATCH", EndpointUser("@me"), data, EndpointUsers, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserConnections returns the user's connections
func (s *Session) UserConnections(options ...RequestOption) (conn []*UserConnection, err error) {
	response, err := s.RequestWithBucketID("GET", EndpointUserConnections("@me"), nil, EndpointUserConnections("@me"), options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(response, &conn)
	if err != nil {
		return
	}

	return
}

// UserChannelCreate creates a new User (Private) Channel with another User
// recipientID : A user ID for the user to which this channel is opened with.
func (s *Session) UserChannelCreate(recipientID string, options ...RequestOption) (st *Channel, err error) {

	data := struct {
		RecipientID string `json:"recipient_id"`
	}{recipientID}

	body, err := s.RequestWithBucketID("POST", EndpointUserChannels("@me"), data, EndpointUserChannels(""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserGuildMember returns a guild member object for the current user in the given Guild.
// guildID : ID of the guild
func (s *Session) UserGuildMember(guildID string, options ...RequestOption) (st *Member, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointUserGuildMember("@me", guildID), nil, EndpointUserGuildMember("@me", guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserGuilds returns an array of UserGuild structures for all guilds.
// limit       : The number guilds that can be returned. (max 200)
// beforeID    : If provided all guilds returned will be before given ID.
// afterID     : If provided all guilds returned will be after given ID.
// withCounts  : Whether to include approximate member and presence counts or not.
func (s *Session) UserGuilds(limit int, beforeID, afterID string, withCounts bool, options ...RequestOption) (st []*UserGuild, err error) {

	v := url.Values{}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if afterID != "" {
		v.Set("after", afterID)
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}
	if withCounts {
		v.Set("with_counts", "true")
	}

	uri := EndpointUserGuilds("@me")

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointUserGuilds(""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserChannelPermissions returns the permission of a user in a channel.
// userID        : The ID of the user to calculate permissions for.
// channelID     : The ID of the channel to calculate permission for.
// fetchOptions  : Options used to fetch guild, member or channel if they are not present in state.
//
// NOTE: This function is now deprecated and will be removed in the future.
// Please see the same function inside state.go
func (s *Session) UserChannelPermissions(userID, channelID string, fetchOptions ...RequestOption) (apermissions int64, err error) {
	// Try to just get permissions from state.
	apermissions, err = s.State.UserChannelPermissions(userID, channelID)
	if err == nil {
		return
	}

	// Otherwise try get as much data from state as possible, falling back to the network.
	channel, err := s.State.Channel(channelID)
	if err != nil || channel == nil {
		channel, err = s.Channel(channelID, fetchOptions...)
		if err != nil {
			return
		}
	}

	permissionsChannel := channel
	thread := channel.IsThread()
	if thread && channel.ParentID != "" {
		permissionsChannel, err = s.State.Channel(channel.ParentID)
		if err != nil || permissionsChannel == nil {
			permissionsChannel, err = s.Channel(channel.ParentID, fetchOptions...)
			if err != nil {
				return
			}
		}
	}

	guildID := permissionsChannel.GuildID
	if guildID == "" {
		guildID = channel.GuildID
	}

	guild, err := s.State.Guild(guildID)
	if err != nil || guild == nil {
		guild, err = s.Guild(guildID, fetchOptions...)
		if err != nil {
			return
		}
	}

	if userID == guild.OwnerID {
		apermissions = PermissionAll
		return
	}

	member, err := s.State.Member(guild.ID, userID)
	if err != nil || member == nil {
		member, err = s.GuildMember(guild.ID, userID, fetchOptions...)
		if err != nil {
			return
		}
	}

	apermissions = memberPermissions(guild, permissionsChannel, userID, member.Roles)
	apermissions = finalizeChannelPermissions(apermissions, member, permissionsChannel.Type, thread)
	return
}

// Calculates the permissions for a member.
// https://support.discord.com/hc/en-us/articles/206141927-How-is-the-permission-hierarchy-structured-
func memberPermissions(guild *Guild, channel *Channel, userID string, roles []string) (apermissions int64) {
	if userID == guild.OwnerID {
		apermissions = PermissionAll
		return
	}

	for _, role := range guild.Roles {
		if role == nil {
			continue
		}
		if role.ID == guild.ID {
			apermissions |= role.Permissions
			break
		}
	}

	for _, role := range guild.Roles {
		if role == nil {
			continue
		}
		for _, roleID := range roles {
			if role.ID == roleID {
				apermissions |= role.Permissions
				break
			}
		}
	}

	if apermissions&PermissionAdministrator == PermissionAdministrator {
		apermissions |= PermissionAll
		return
	}

	// Apply @everyone overrides from the channel.
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite == nil {
			continue
		}
		if guild.ID == overwrite.ID {
			apermissions &= ^overwrite.Deny
			apermissions |= overwrite.Allow
			break
		}
	}

	var denies, allows int64
	// Member overwrites can override role overrides, so do two passes
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite == nil {
			continue
		}
		for _, roleID := range roles {
			if overwrite.Type == PermissionOverwriteTypeRole && roleID == overwrite.ID {
				denies |= overwrite.Deny
				allows |= overwrite.Allow
				break
			}
		}
	}

	apermissions &= ^denies
	apermissions |= allows

	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite == nil {
			continue
		}
		if overwrite.Type == PermissionOverwriteTypeMember && overwrite.ID == userID {
			apermissions &= ^overwrite.Deny
			apermissions |= overwrite.Allow
			break
		}
	}

	if apermissions&PermissionAdministrator == PermissionAdministrator {
		apermissions |= PermissionAllChannel
	}

	return apermissions
}

const (
	permissionAllChannelImplicit = PermissionAllChannel &^ (PermissionViewAuditLogs |
		PermissionManageEvents |
		PermissionCreateEvents)
	permissionAllVoiceImplicit = PermissionVoicePrioritySpeaker |
		PermissionVoiceStreamVideo |
		PermissionVoiceConnect |
		PermissionVoiceSpeak |
		PermissionVoiceMuteMembers |
		PermissionVoiceDeafenMembers |
		PermissionVoiceMoveMembers |
		PermissionVoiceUseVAD |
		PermissionUseEmbeddedActivities |
		PermissionUseSoundboard |
		PermissionUseExternalSounds |
		PermissionSetVoiceChannelStatus
)

func finalizeChannelPermissions(apermissions int64, member *Member, channelType ChannelType, thread bool) int64 {
	if apermissions&PermissionAdministrator == PermissionAdministrator {
		return apermissions
	}

	sendPermission := int64(PermissionSendMessages)
	if thread {
		apermissions &^= PermissionSendMessages
		sendPermission = PermissionSendMessagesInThreads
	}

	if apermissions&sendPermission == 0 {
		apermissions &^= PermissionSendTTSMessages |
			PermissionMentionEveryone |
			PermissionEmbedLinks |
			PermissionAttachFiles
	}
	if apermissions&PermissionViewChannel == 0 {
		apermissions &^= permissionAllChannelImplicit
	}
	isVoice := channelType == ChannelTypeGuildVoice || channelType == ChannelTypeGuildStageVoice
	if isVoice && apermissions&PermissionVoiceConnect == 0 {
		apermissions &^= permissionAllVoiceImplicit | PermissionManageChannels | PermissionManageRoles
	}
	isTimedOut := member != nil &&
		member.CommunicationDisabledUntil != nil &&
		member.CommunicationDisabledUntil.After(time.Now())
	if isTimedOut {
		apermissions &= PermissionViewChannel | PermissionReadMessageHistory
	}

	return apermissions
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Guilds
// ------------------------------------------------------------------------------------------------

// Guild returns a Guild structure of a specific Guild.
// guildID   : The ID of a Guild
func (s *Session) Guild(guildID string, options ...RequestOption) (st *Guild, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuild(guildID), nil, EndpointGuild(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildWithCounts returns a Guild structure of a specific Guild with approximate member and presence counts.
// guildID    : The ID of a Guild
func (s *Session) GuildWithCounts(guildID string, options ...RequestOption) (st *Guild, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuild(guildID)+"?with_counts=true", nil, EndpointGuild(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildPreview returns a GuildPreview structure of a specific public Guild.
// guildID   : The ID of a Guild
func (s *Session) GuildPreview(guildID string, options ...RequestOption) (st *GuildPreview, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildPreview(guildID), nil, EndpointGuildPreview(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildCreate creates a new Guild
// name      : A name for the Guild (2-100 characters)
func (s *Session) GuildCreate(name string, options ...RequestOption) (st *Guild, err error) {

	data := struct {
		Name string `json:"name"`
	}{name}

	body, err := s.RequestWithBucketID("POST", EndpointGuildCreate, data, EndpointGuildCreate, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildEdit edits a new Guild
// guildID   : The ID of a Guild
// g 		 : A GuildParams struct with the values Name, Region and VerificationLevel defined.
func (s *Session) GuildEdit(guildID string, g *GuildParams, options ...RequestOption) (st *Guild, err error) {

	// Bounds checking for VerificationLevel, interval: [0, 4]
	if g.VerificationLevel != nil {
		val := *g.VerificationLevel
		if val < 0 || val > 4 {
			err = ErrVerificationLevelBounds
			return
		}
	}

	// Bounds checking for regions
	if g.Region != "" {
		isValid := false
		regions, _ := s.VoiceRegions(options...)
		for _, r := range regions {
			if g.Region == r.ID {
				isValid = true
			}
		}
		if !isValid {
			var valid []string
			for _, r := range regions {
				valid = append(valid, r.ID)
			}
			err = fmt.Errorf("Region not a valid region (%q)", valid)
			return
		}
	}

	body, err := s.RequestWithBucketID("PATCH", EndpointGuild(guildID), g, EndpointGuild(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildDelete deletes a Guild.
// guildID   : The ID of a Guild
func (s *Session) GuildDelete(guildID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointGuild(guildID), nil, EndpointGuild(guildID), options...)
	return
}

// GuildLeave leaves a Guild.
// guildID   : The ID of a Guild
func (s *Session) GuildLeave(guildID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointUserGuild("@me", guildID), nil, EndpointUserGuild("", guildID), options...)
	return
}

// GuildJoinRequests returns a page of join requests for a guild.
func (s *Session) GuildJoinRequests(guildID string, options ...RequestOption) (*GuildJoinRequestsResult, error) {
	return s.GuildJoinRequestsWithOptions(guildID, nil, options...)
}

// GuildJoinRequestsWithOptions returns a filtered page of join requests for a guild.
func (s *Session) GuildJoinRequestsWithOptions(guildID string, query *GuildJoinRequestsOptions, options ...RequestOption) (*GuildJoinRequestsResult, error) {
	endpoint := EndpointGuildJoinRequests(guildID)
	uri := endpoint
	if query != nil {
		values := url.Values{}
		if query.Status != "" {
			values.Set("status", string(query.Status))
		}
		if query.Limit > 0 {
			values.Set("limit", strconv.Itoa(query.Limit))
		}
		if query.Before != "" {
			values.Set("before", query.Before)
		}
		if query.After != "" {
			values.Set("after", query.After)
		}
		if encoded := values.Encode(); encoded != "" {
			uri += "?" + encoded
		}
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	var result *GuildJoinRequestsResult
	if err = unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("%w: guild join requests response is null", ErrJSONUnmarshal)
	}
	if result.GuildJoinRequests == nil {
		return nil, fmt.Errorf("%w: guild join requests response is missing requests", ErrJSONUnmarshal)
	}
	for _, request := range result.GuildJoinRequests {
		if request == nil {
			return nil, fmt.Errorf("%w: guild join requests response contains a null request", ErrJSONUnmarshal)
		}
	}
	return result, nil
}

// GuildJoinRequestAction approves or rejects a guild join request.
func (s *Session) GuildJoinRequestAction(guildID, requestID string, data *GuildJoinRequestActionParams, options ...RequestOption) (*GuildJoinRequest, error) {
	endpoint := EndpointGuildJoinRequest(guildID, requestID)
	body, err := s.RequestWithBucketID("PATCH", endpoint, data, EndpointGuildJoinRequests(guildID), options...)
	if err != nil {
		return nil, err
	}

	var request *GuildJoinRequest
	if err = unmarshal(body, &request); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, fmt.Errorf("%w: guild join request response is null", ErrJSONUnmarshal)
	}
	return request, nil
}

// GuildBans returns an array of GuildBan structures for bans in the given guild.
// guildID   : The ID of a Guild
// limit     : Max number of bans to return (max 1000)
// beforeID  : If not empty all returned users will be after the given id
// afterID   : If not empty all returned users will be before the given id
func (s *Session) GuildBans(guildID string, limit int, beforeID, afterID string, options ...RequestOption) (st []*GuildBan, err error) {
	uri := EndpointGuildBans(guildID)

	v := url.Values{}
	if limit != 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}
	if afterID != "" {
		v.Set("after", afterID)
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildBans(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildBanCreate bans the given user from the given guild.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
// days      : The number of days of previous comments to delete.
func (s *Session) GuildBanCreate(guildID, userID string, days int, options ...RequestOption) (err error) {
	return s.GuildBanCreateWithReason(guildID, userID, "", days, options...)
}

// GuildBan finds ban by given guild and user id and returns GuildBan structure
func (s *Session) GuildBan(guildID, userID string, options ...RequestOption) (st *GuildBan, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildBan(guildID, userID), nil, EndpointGuildBan(guildID, userID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildBanCreateWithReason bans the given user from the given guild also providing a reaso.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
// reason    : The reason for this ban
// days      : The number of days of previous comments to delete.
func (s *Session) GuildBanCreateWithReason(guildID, userID, reason string, days int, options ...RequestOption) (err error) {

	uri := EndpointGuildBan(guildID, userID)

	queryParams := url.Values{}
	if days > 0 {
		queryParams.Set("delete_message_days", strconv.Itoa(days))
	}
	if reason != "" {
		queryParams.Set("reason", reason)
	}

	if len(queryParams) > 0 {
		uri += "?" + queryParams.Encode()
	}

	_, err = s.RequestWithBucketID("PUT", uri, nil, EndpointGuildBan(guildID, ""), options...)
	return
}

// GuildBanDelete removes the given user from the guild bans
// guildID   : The ID of a Guild.
// userID    : The ID of a User
func (s *Session) GuildBanDelete(guildID, userID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointGuildBan(guildID, userID), nil, EndpointGuildBan(guildID, ""), options...)
	return
}

// GuildBulkBan bans up to 200 users from the guild.
// guildID : The ID of a Guild.
// data    : The users to ban and number of seconds of messages to delete.
func (s *Session) GuildBulkBan(guildID string, data *GuildBulkBanParams, options ...RequestOption) (result *GuildBulkBanResult, err error) {
	if data == nil {
		return nil, fmt.Errorf("guild bulk ban data cannot be nil")
	}
	if len(data.UserIDs) == 0 {
		return nil, fmt.Errorf("guild bulk ban user IDs cannot be empty")
	}

	endpoint := EndpointGuildBulkBan(guildID)
	body, err := s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &result)
	if err != nil {
		result = nil
		return
	}
	if result == nil {
		err = fmt.Errorf("%w: guild bulk ban response is null", ErrJSONUnmarshal)
		return
	}
	if result.BannedUsers == nil {
		result = nil
		err = fmt.Errorf("%w: guild bulk ban response is missing banned users", ErrJSONUnmarshal)
		return
	}
	if result.FailedUsers == nil {
		result = nil
		err = fmt.Errorf("%w: guild bulk ban response is missing failed users", ErrJSONUnmarshal)
	}
	return
}

func validateGuildMember(member *Member) error {
	if member == nil {
		return fmt.Errorf("%w: guild member response is null", ErrJSONUnmarshal)
	}
	if member.User == nil {
		return fmt.Errorf("%w: guild member response is missing user", ErrJSONUnmarshal)
	}

	return nil
}

// GuildMembers returns a list of members for a guild.
// guildID  : The ID of a Guild.
// after    : The id of the member to return members after
// limit    : max number of members to return (max 1000)
func (s *Session) GuildMembers(guildID string, after string, limit int, options ...RequestOption) (st []*Member, err error) {

	uri := EndpointGuildMembers(guildID)

	v := url.Values{}

	if after != "" {
		v.Set("after", after)
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildMembers(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	if err != nil {
		st = nil
		return
	}
	if st == nil {
		err = fmt.Errorf("%w: guild members response is null", ErrJSONUnmarshal)
		return
	}
	// The returned objects don't have the GuildID attribute so we will set it here.
	for _, member := range st {
		if err = validateGuildMember(member); err != nil {
			st = nil
			return
		}
		member.GuildID = guildID
	}
	return
}

// GuildMembersSearch returns a list of guild member objects whose username or nickname starts with a provided string
// guildID  : The ID of a Guild
// query    : Query string to match username(s) and nickname(s) against
// limit    : Max number of members to return (default 1, min 1, max 1000)
func (s *Session) GuildMembersSearch(guildID, query string, limit int, options ...RequestOption) (st []*Member, err error) {

	uri := EndpointGuildMembersSearch(guildID)

	queryParams := url.Values{}
	queryParams.Set("query", query)
	if limit > 1 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}

	body, err := s.RequestWithBucketID("GET", uri+"?"+queryParams.Encode(), nil, uri, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	if err != nil {
		st = nil
		return
	}
	if st == nil {
		err = fmt.Errorf("%w: guild members response is null", ErrJSONUnmarshal)
		return
	}
	// The returned objects don't have the GuildID attribute so we will set it here.
	for _, member := range st {
		if err = validateGuildMember(member); err != nil {
			st = nil
			return
		}
		member.GuildID = guildID
	}
	return
}

// GuildMessagesSearch returns messages matching the search options in a guild.
// guildID : The ID of a Guild.
// opts    : Search query options.
func (s *Session) GuildMessagesSearch(guildID string, opts *GuildMessagesSearchOptions, options ...RequestOption) (st *GuildMessagesSearchResult, err error) {
	endpoint := EndpointGuildMessagesSearch(guildID)
	uri := endpoint

	if opts != nil {
		queryParams := url.Values{}
		addValues := func(key string, values []string) {
			for _, value := range values {
				queryParams.Add(key, value)
			}
		}

		if opts.Limit > 0 {
			queryParams.Set("limit", strconv.Itoa(opts.Limit))
		}
		if opts.Offset > 0 {
			queryParams.Set("offset", strconv.Itoa(opts.Offset))
		}
		if opts.MaxID != "" {
			queryParams.Set("max_id", opts.MaxID)
		}
		if opts.MinID != "" {
			queryParams.Set("min_id", opts.MinID)
		}
		if opts.Slop > 0 {
			queryParams.Set("slop", strconv.Itoa(opts.Slop))
		}
		if opts.Content != "" {
			queryParams.Set("content", opts.Content)
		}
		addValues("channel_id", opts.ChannelIDs)
		addValues("author_type", opts.AuthorTypes)
		addValues("author_id", opts.AuthorIDs)
		addValues("mentions", opts.Mentions)
		addValues("mentions_role_id", opts.MentionRoleIDs)
		if opts.MentionEveryone != nil {
			queryParams.Set("mention_everyone", strconv.FormatBool(*opts.MentionEveryone))
		}
		addValues("replied_to_user_id", opts.RepliedToUserIDs)
		addValues("replied_to_message_id", opts.RepliedToMessageIDs)
		if opts.Pinned != nil {
			queryParams.Set("pinned", strconv.FormatBool(*opts.Pinned))
		}
		addValues("has", opts.Has)
		addValues("embed_type", opts.EmbedTypes)
		addValues("embed_provider", opts.EmbedProviders)
		addValues("link_hostname", opts.LinkHostnames)
		addValues("attachment_filename", opts.AttachmentFilenames)
		addValues("attachment_extension", opts.AttachmentExtensions)
		if opts.SortBy != "" {
			queryParams.Set("sort_by", opts.SortBy)
		}
		if opts.SortOrder != "" {
			queryParams.Set("sort_order", opts.SortOrder)
		}
		if opts.IncludeNSFW != nil {
			queryParams.Set("include_nsfw", strconv.FormatBool(*opts.IncludeNSFW))
		}
		if len(queryParams) > 0 {
			uri += "?" + queryParams.Encode()
		}
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildMember returns a member of a guild.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
func (s *Session) GuildMember(guildID, userID string, options ...RequestOption) (st *Member, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildMember(guildID, userID), nil, EndpointGuildMember(guildID, ""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	if err != nil {
		st = nil
		return
	}
	if err = validateGuildMember(st); err != nil {
		st = nil
		return
	}
	// The returned object doesn't have the GuildID attribute so we will set it here.
	st.GuildID = guildID
	return
}

// GuildMemberAdd force joins a user to the guild.
// guildID       : The ID of a Guild.
// userID        : The ID of a User.
// data          : Parameters of the user to add.
func (s *Session) GuildMemberAdd(guildID, userID string, data *GuildMemberAddParams, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("PUT", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""), options...)
	if err != nil {
		return err
	}

	return err
}

// GuildMemberDelete removes the given user from the given guild.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
func (s *Session) GuildMemberDelete(guildID, userID string, options ...RequestOption) (err error) {

	return s.GuildMemberDeleteWithReason(guildID, userID, "", options...)
}

// GuildMemberDeleteWithReason removes the given user from the given guild.
// guildID   : The ID of a Guild.
// userID    : The ID of a User
// reason    : The reason for the kick
func (s *Session) GuildMemberDeleteWithReason(guildID, userID, reason string, options ...RequestOption) (err error) {

	uri := EndpointGuildMember(guildID, userID)
	if reason != "" {
		uri += "?reason=" + url.QueryEscape(reason)
	}

	_, err = s.RequestWithBucketID("DELETE", uri, nil, EndpointGuildMember(guildID, ""), options...)
	return
}

// GuildMemberEdit edits and returns updated member.
// guildID  : The ID of a Guild.
// userID   : The ID of a User.
// data     : Updated GuildMember data.
func (s *Session) GuildMemberEdit(guildID, userID string, data *GuildMemberParams, options ...RequestOption) (st *Member, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""), options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &st)
	return
}

// GuildCurrentMemberEdit edits and returns the current member in a guild.
// guildID : The ID of a Guild.
// data    : Updated current member data.
func (s *Session) GuildCurrentMemberEdit(guildID string, data *GuildCurrentMemberParams, options ...RequestOption) (st *Member, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointGuildCurrentMember(guildID), data, EndpointGuildMember(guildID, ""), options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &st); err != nil {
		return nil, err
	}
	if err = validateGuildMember(st); err != nil {
		return nil, err
	}
	st.GuildID = guildID
	return st, nil
}

// GuildMemberEditComplex edits the nickname and roles of a member.
// NOTE: deprecated, use GuildMemberEdit instead.
//
// guildID  : The ID of a Guild.
// userID   : The ID of a User.
// data     : A GuildMemberEditData struct with the new nickname and roles
func (s *Session) GuildMemberEditComplex(guildID, userID string, data *GuildMemberParams, options ...RequestOption) (st *Member, err error) {
	return s.GuildMemberEdit(guildID, userID, data, options...)
}

// GuildMemberMove moves a guild member from one voice channel to another/none
// guildID   : The ID of a Guild.
// userID    : The ID of a User.
// channelID : The ID of a channel to move user to or nil to remove from voice channel
//
// NOTE : I am not entirely set on the name of this function and it may change
// prior to the final 1.0.0 release of Discordgo
func (s *Session) GuildMemberMove(guildID string, userID string, channelID *string, options ...RequestOption) (err error) {
	data := struct {
		ChannelID *string `json:"channel_id"`
	}{channelID}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""), options...)
	return
}

// GuildMemberNickname updates the nickname of a guild member
// guildID   : The ID of a guild
// userID    : The ID of a user
// userID    : The ID of a user or "@me" which is a shortcut of the current user ID
// nickname  : The nickname of the member, "" will reset their nickname
func (s *Session) GuildMemberNickname(guildID, userID, nickname string, options ...RequestOption) (err error) {

	data := struct {
		Nick string `json:"nick"`
	}{nickname}

	if userID == "@me" {
		userID += "/nick"
	}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""), options...)
	return
}

// GuildMemberMute server mutes a guild member
// guildID   : The ID of a Guild.
// userID    : The ID of a User.
// mute      : boolean value for if the user should be muted
func (s *Session) GuildMemberMute(guildID string, userID string, mute bool, options ...RequestOption) (err error) {
	data := struct {
		Mute bool `json:"mute"`
	}{mute}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""), options...)
	return
}

// GuildMemberTimeout times out a guild member
// guildID   : The ID of a Guild.
// userID    : The ID of a User.
// until     : The timestamp for how long a member should be timed out. Set to nil to remove timeout.
func (s *Session) GuildMemberTimeout(guildID string, userID string, until *time.Time, options ...RequestOption) (err error) {
	data := struct {
		CommunicationDisabledUntil *time.Time `json:"communication_disabled_until"`
	}{until}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""), options...)
	return
}

// GuildMemberDeafen server deafens a guild member
// guildID   : The ID of a Guild.
// userID    : The ID of a User.
// deaf      : boolean value for if the user should be deafened
func (s *Session) GuildMemberDeafen(guildID string, userID string, deaf bool, options ...RequestOption) (err error) {
	data := struct {
		Deaf bool `json:"deaf"`
	}{deaf}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildMember(guildID, userID), data, EndpointGuildMember(guildID, ""), options...)
	return
}

// GuildMemberRoleAdd adds the specified role to a given member
// guildID   : The ID of a Guild.
// userID    : The ID of a User.
// roleID    : The ID of a Role to be assigned to the user.
func (s *Session) GuildMemberRoleAdd(guildID, userID, roleID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("PUT", EndpointGuildMemberRole(guildID, userID, roleID), nil, EndpointGuildMemberRole(guildID, "", ""), options...)

	return
}

// GuildMemberRoleRemove removes the specified role to a given member
// guildID   : The ID of a Guild.
// userID    : The ID of a User.
// roleID    : The ID of a Role to be removed from the user.
func (s *Session) GuildMemberRoleRemove(guildID, userID, roleID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointGuildMemberRole(guildID, userID, roleID), nil, EndpointGuildMemberRole(guildID, "", ""), options...)

	return
}

// GuildChannels returns an array of Channel structures for all channels of a
// given guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildChannels(guildID string, options ...RequestOption) (st []*Channel, err error) {

	body, err := s.RequestRaw("GET", EndpointGuildChannels(guildID), "", nil, EndpointGuildChannels(guildID), 0, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildChannelCreateData is provided to GuildChannelCreateComplex
type GuildChannelCreateData struct {
	Name                 string                 `json:"name"`
	Type                 ChannelType            `json:"type"`
	Topic                string                 `json:"topic,omitempty"`
	Bitrate              int                    `json:"bitrate,omitempty"`
	UserLimit            int                    `json:"user_limit,omitempty"`
	RateLimitPerUser     int                    `json:"rate_limit_per_user,omitempty"`
	Position             int                    `json:"position,omitempty"`
	PermissionOverwrites []*PermissionOverwrite `json:"permission_overwrites,omitempty"`
	ParentID             string                 `json:"parent_id,omitempty"`
	NSFW                 bool                   `json:"nsfw,omitempty"`
}

// GuildChannelCreateComplex creates a new channel in the given guild
// guildID      : The ID of a Guild
// data         : A data struct describing the new Channel, Name and Type are mandatory, other fields depending on the type
func (s *Session) GuildChannelCreateComplex(guildID string, data GuildChannelCreateData, options ...RequestOption) (st *Channel, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildChannels(guildID), data, EndpointGuildChannels(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildChannelCreate creates a new channel in the given guild
// guildID   : The ID of a Guild.
// name      : Name of the channel (2-100 chars length)
// ctype     : Type of the channel
func (s *Session) GuildChannelCreate(guildID, name string, ctype ChannelType, options ...RequestOption) (st *Channel, err error) {
	return s.GuildChannelCreateComplex(guildID, GuildChannelCreateData{
		Name: name,
		Type: ctype,
	}, options...)
}

// GuildChannelsReorder updates the order of channels in a guild
// guildID   : The ID of a Guild.
// channels  : Updated channels.
func (s *Session) GuildChannelsReorder(guildID string, channels []*Channel, options ...RequestOption) (err error) {

	data := make([]struct {
		ID       string `json:"id"`
		Position int    `json:"position"`
	}, len(channels))

	for i, c := range channels {
		data[i].ID = c.ID
		data[i].Position = c.Position
	}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildChannels(guildID), data, EndpointGuildChannels(guildID), options...)
	return
}

// GuildInvites returns an array of Invite structures for the given guild
// guildID   : The ID of a Guild.
func (s *Session) GuildInvites(guildID string, options ...RequestOption) (st []*Invite, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildInvites(guildID), nil, EndpointGuildInvites(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildVanityURL returns an Invite containing the vanity URL code and use count for the given guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildVanityURL(guildID string, options ...RequestOption) (st *Invite, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildVanityURL(guildID), nil, EndpointGuildVanityURL(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildWidget returns the public widget data for a guild.
func (s *Session) GuildWidget(guildID string, options ...RequestOption) (*GuildWidget, error) {
	endpoint := EndpointGuildWidgetJSON(guildID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	var widget *GuildWidget
	if err = unmarshal(body, &widget); err != nil {
		return nil, err
	}
	if widget == nil {
		return nil, fmt.Errorf("%w: guild widget response is null", ErrJSONUnmarshal)
	}
	return widget, nil
}

// GuildRoles returns all roles for a given guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildRoles(guildID string, options ...RequestOption) (st []*Role, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildRoles(guildID), nil, EndpointGuildRoles(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return // TODO return pointer
}

// GuildRole returns a specific role for a given guild.
// guildID   : The ID of a Guild.
// roleID    : The ID of a Role.
func (s *Session) GuildRole(guildID, roleID string, options ...RequestOption) (st *Role, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildRole(guildID, roleID), nil, EndpointGuildRole(guildID, ""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildRoleCreate creates a new Guild Role and returns it.
// guildID : The ID of a Guild.
// data    : New Role parameters.
func (s *Session) GuildRoleCreate(guildID string, data *RoleParams, options ...RequestOption) (st *Role, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildRoles(guildID), data, EndpointGuildRoles(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildRoleEdit updates an existing Guild Role and returns updated Role data.
// guildID   : The ID of a Guild.
// roleID    : The ID of a Role.
// data 		 : Updated Role data.
func (s *Session) GuildRoleEdit(guildID, roleID string, data *RoleParams, options ...RequestOption) (st *Role, err error) {
	if data == nil {
		return nil, fmt.Errorf("role data cannot be nil")
	}

	// Prevent sending a color int that is too big.
	if data.Color != nil && *data.Color > 0xFFFFFF {
		return nil, fmt.Errorf("color value cannot be larger than 0xFFFFFF")
	}

	body, err := s.RequestWithBucketID("PATCH", EndpointGuildRole(guildID, roleID), data, EndpointGuildRole(guildID, ""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildRoleReorder reoders guild roles
// guildID   : The ID of a Guild.
// roles     : A list of ordered roles.
func (s *Session) GuildRoleReorder(guildID string, roles []*Role, options ...RequestOption) (st []*Role, err error) {

	body, err := s.RequestWithBucketID("PATCH", EndpointGuildRoles(guildID), roles, EndpointGuildRoles(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildRoleDelete deletes an existing role.
// guildID   : The ID of a Guild.
// roleID    : The ID of a Role.
func (s *Session) GuildRoleDelete(guildID, roleID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointGuildRole(guildID, roleID), nil, EndpointGuildRole(guildID, ""), options...)

	return
}

// GuildRoleMemberCounts returns a map of role ID to the number of members that have that role.
//
// guildID	: The ID of a Guild.
//
// Does not include the @everyone role.
func (s *Session) GuildRoleMemberCounts(guildID string, options ...RequestOption) (memberCounts map[string]uint64, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildRoleMemberCounts(guildID), nil, EndpointGuildRoleMemberCounts(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &memberCounts)
	return
}

// GuildPruneCount Returns the number of members that would be removed in a prune operation.
// Requires 'KICK_MEMBER' permission.
// guildID	: The ID of a Guild.
// days		: The number of days to count prune for (1 or more).
func (s *Session) GuildPruneCount(guildID string, days uint32, options ...RequestOption) (count uint32, err error) {
	count = 0

	if days <= 0 {
		err = ErrPruneDaysBounds
		return
	}

	p := struct {
		Pruned uint32 `json:"pruned"`
	}{}

	uri := EndpointGuildPrune(guildID) + "?days=" + strconv.FormatUint(uint64(days), 10)
	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildPrune(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &p)
	if err != nil {
		return
	}

	count = p.Pruned

	return
}

// GuildPrune Begin as prune operation. Requires the 'KICK_MEMBERS' permission.
// Returns an object with one 'pruned' key indicating the number of members that were removed in the prune operation.
// guildID	: The ID of a Guild.
// days		: The number of days to count prune for (1 or more).
func (s *Session) GuildPrune(guildID string, days uint32, options ...RequestOption) (count uint32, err error) {

	count = 0

	if days <= 0 {
		err = ErrPruneDaysBounds
		return
	}

	data := struct {
		days uint32
	}{days}

	p := struct {
		Pruned uint32 `json:"pruned"`
	}{}

	body, err := s.RequestWithBucketID("POST", EndpointGuildPrune(guildID), data, EndpointGuildPrune(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &p)
	if err != nil {
		return
	}

	count = p.Pruned

	return
}

// GuildIntegrations returns an array of Integrations for a guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildIntegrations(guildID string, options ...RequestOption) (st []*Integration, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildIntegrations(guildID), nil, EndpointGuildIntegrations(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildIntegrationCreate creates a Guild Integration.
// guildID          : The ID of a Guild.
// integrationType  : The Integration type.
// integrationID    : The ID of an integration.
func (s *Session) GuildIntegrationCreate(guildID, integrationType, integrationID string, options ...RequestOption) (err error) {

	data := struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}{integrationType, integrationID}

	_, err = s.RequestWithBucketID("POST", EndpointGuildIntegrations(guildID), data, EndpointGuildIntegrations(guildID), options...)
	return
}

// GuildIntegrationEdit edits a Guild Integration.
// guildID              : The ID of a Guild.
// integrationType      : The Integration type.
// integrationID        : The ID of an integration.
// expireBehavior	      : The behavior when an integration subscription lapses (see the integration object documentation).
// expireGracePeriod    : Period (in seconds) where the integration will ignore lapsed subscriptions.
// enableEmoticons	    : Whether emoticons should be synced for this integration (twitch only currently).
func (s *Session) GuildIntegrationEdit(guildID, integrationID string, expireBehavior, expireGracePeriod int, enableEmoticons bool, options ...RequestOption) (err error) {

	data := struct {
		ExpireBehavior    int  `json:"expire_behavior"`
		ExpireGracePeriod int  `json:"expire_grace_period"`
		EnableEmoticons   bool `json:"enable_emoticons"`
	}{expireBehavior, expireGracePeriod, enableEmoticons}

	_, err = s.RequestWithBucketID("PATCH", EndpointGuildIntegration(guildID, integrationID), data, EndpointGuildIntegration(guildID, ""), options...)
	return
}

// GuildIntegrationDelete removes the given integration from the Guild.
// guildID          : The ID of a Guild.
// integrationID    : The ID of an integration.
func (s *Session) GuildIntegrationDelete(guildID, integrationID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointGuildIntegration(guildID, integrationID), nil, EndpointGuildIntegration(guildID, ""), options...)
	return
}

// GuildIcon returns an image.Image of a guild icon.
// guildID   : The ID of a Guild.
func (s *Session) GuildIcon(guildID string, options ...RequestOption) (img image.Image, err error) {
	g, err := s.Guild(guildID, options...)
	if err != nil {
		return
	}

	if g.Icon == "" {
		err = ErrGuildNoIcon
		return
	}

	body, err := s.RequestWithBucketID("GET", EndpointGuildIcon(guildID, g.Icon), nil, EndpointGuildIcon(guildID, ""), options...)
	if err != nil {
		return
	}

	img, _, err = image.Decode(bytes.NewReader(body))
	return
}

// GuildSplash returns an image.Image of a guild splash image.
// guildID   : The ID of a Guild.
func (s *Session) GuildSplash(guildID string, options ...RequestOption) (img image.Image, err error) {
	g, err := s.Guild(guildID, options...)
	if err != nil {
		return
	}

	if g.Splash == "" {
		err = ErrGuildNoSplash
		return
	}

	body, err := s.RequestWithBucketID("GET", EndpointGuildSplash(guildID, g.Splash), nil, EndpointGuildSplash(guildID, ""), options...)
	if err != nil {
		return
	}

	img, _, err = image.Decode(bytes.NewReader(body))
	return
}

// GuildEmbed returns the embed for a Guild.
// guildID   : The ID of a Guild.
func (s *Session) GuildEmbed(guildID string, options ...RequestOption) (st *GuildEmbed, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildEmbed(guildID), nil, EndpointGuildEmbed(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildEmbedEdit edits the embed of a Guild.
// guildID   : The ID of a Guild.
// data      : New GuildEmbed data.
func (s *Session) GuildEmbedEdit(guildID string, data *GuildEmbed, options ...RequestOption) (err error) {
	_, err = s.RequestWithBucketID("PATCH", EndpointGuildEmbed(guildID), data, EndpointGuildEmbed(guildID), options...)
	return
}

// GuildAuditLog returns the audit log for a Guild.
// guildID     : The ID of a Guild.
// userID      : If provided the log will be filtered for the given ID.
// beforeID    : If provided all log entries returned will be before the given ID.
// actionType  : If provided the log will be filtered for the given Action Type.
// limit       : The number messages that can be returned. (default 50, min 1, max 100)
func (s *Session) GuildAuditLog(guildID, userID, beforeID string, actionType, limit int, options ...RequestOption) (st *GuildAuditLog, err error) {

	uri := EndpointGuildAuditLogs(guildID)

	v := url.Values{}
	if userID != "" {
		v.Set("user_id", userID)
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}
	if actionType > 0 {
		v.Set("action_type", strconv.Itoa(actionType))
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if len(v) > 0 {
		uri = fmt.Sprintf("%s?%s", uri, v.Encode())
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildAuditLogs(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildEmojis returns all emoji
// guildID : The ID of a Guild.
func (s *Session) GuildEmojis(guildID string, options ...RequestOption) (emoji []*Emoji, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildEmojis(guildID), nil, EndpointGuildEmojis(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// GuildEmoji returns specified emoji.
// guildID : The ID of a Guild
// emojiID : The ID of an Emoji to retrieve
func (s *Session) GuildEmoji(guildID, emojiID string, options ...RequestOption) (emoji *Emoji, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointGuildEmoji(guildID, emojiID), nil, EndpointGuildEmoji(guildID, emojiID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// GuildEmojiCreate creates a new Emoji.
// guildID : The ID of a Guild.
// data    : New Emoji data.
func (s *Session) GuildEmojiCreate(guildID string, data *EmojiParams, options ...RequestOption) (emoji *Emoji, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildEmojis(guildID), data, EndpointGuildEmojis(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// GuildEmojiEdit modifies and returns updated Emoji.
// guildID : The ID of a Guild.
// emojiID : The ID of an Emoji.
// data    : Updated Emoji data.
func (s *Session) GuildEmojiEdit(guildID, emojiID string, data *EmojiParams, options ...RequestOption) (emoji *Emoji, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointGuildEmoji(guildID, emojiID), data, EndpointGuildEmojis(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// GuildEmojiDelete deletes an Emoji.
// guildID : The ID of a Guild.
// emojiID : The ID of an Emoji.
func (s *Session) GuildEmojiDelete(guildID, emojiID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointGuildEmoji(guildID, emojiID), nil, EndpointGuildEmojis(guildID), options...)
	return
}

// Sticker returns the sticker for the given ID.
func (s *Session) Sticker(stickerID string, options ...RequestOption) (sticker *Sticker, err error) {
	endpoint := EndpointSticker(stickerID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, EndpointStickers, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &sticker)
	return
}

// StickerPacks returns all available sticker packs.
func (s *Session) StickerPacks(options ...RequestOption) (packs []*StickerPack, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointStickerPacks, nil, EndpointStickerPacks, options...)
	if err != nil {
		return nil, err
	}

	var response struct {
		StickerPacks []*StickerPack `json:"sticker_packs"`
	}
	if err = unmarshal(body, &response); err != nil {
		return nil, err
	}

	return response.StickerPacks, nil
}

// StickerPack returns the sticker pack for the given ID.
func (s *Session) StickerPack(packID string, options ...RequestOption) (pack *StickerPack, err error) {
	endpoint := EndpointStickerPack(packID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, EndpointStickerPacks, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &pack)
	return
}

// GuildStickers returns all stickers for the given guild.
func (s *Session) GuildStickers(guildID string, options ...RequestOption) (stickers []*Sticker, err error) {
	endpoint := EndpointGuildStickers(guildID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &stickers)
	return
}

// GuildSticker returns the specified guild sticker.
func (s *Session) GuildSticker(guildID, stickerID string, options ...RequestOption) (sticker *Sticker, err error) {
	endpoint := EndpointGuildSticker(guildID, stickerID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &sticker)
	return
}

// GuildStickerCreate creates a guild sticker.
func (s *Session) GuildStickerCreate(guildID string, data *GuildStickerCreateParams, options ...RequestOption) (sticker *Sticker, err error) {
	if data == nil {
		return nil, fmt.Errorf("guild sticker data cannot be nil")
	}
	if data.File == nil {
		return nil, fmt.Errorf("guild sticker file cannot be nil")
	}

	upload := *data.File
	upload.FieldName = "file"
	fields := map[string]string{
		"name":        data.Name,
		"description": data.Description,
		"tags":        data.Tags,
	}
	contentType, requestBody, err := multipartBody(nil, fields, []*File{&upload}, false)
	if err != nil {
		return nil, err
	}

	endpoint := EndpointGuildStickers(guildID)
	body, err := s.RequestRaw("POST", endpoint, contentType, requestBody, endpoint, 0, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &sticker)
	return
}

// GuildStickerEdit modifies the specified guild sticker.
func (s *Session) GuildStickerEdit(guildID, stickerID string, data *GuildStickerEditParams, options ...RequestOption) (sticker *Sticker, err error) {
	if data == nil {
		return nil, fmt.Errorf("guild sticker data cannot be nil")
	}

	endpoint := EndpointGuildSticker(guildID, stickerID)
	body, err := s.RequestWithBucketID("PATCH", endpoint, data, EndpointGuildStickers(guildID), options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &sticker)
	return
}

// GuildStickerDelete deletes the specified guild sticker.
func (s *Session) GuildStickerDelete(guildID, stickerID string, options ...RequestOption) error {
	_, err := s.RequestWithBucketID("DELETE", EndpointGuildSticker(guildID, stickerID), nil, EndpointGuildStickers(guildID), options...)
	return err
}

// SoundboardSoundSend sends a soundboard sound to a voice channel.
// channelID     : The ID of a voice Channel.
// soundID       : The ID of the soundboard sound to play.
// sourceGuildID : The ID of the guild the soundboard sound is from, if different from the channel's guild.
func (s *Session) SoundboardSoundSend(channelID, soundID, sourceGuildID string, options ...RequestOption) (err error) {
	endpoint := EndpointChannelSendSoundboardSound(channelID)
	data := struct {
		SoundID       string `json:"sound_id"`
		SourceGuildID string `json:"source_guild_id,omitempty"`
	}{soundID, sourceGuildID}

	_, err = s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	return
}

// SoundboardDefaultSounds returns all default soundboard sounds.
func (s *Session) SoundboardDefaultSounds(options ...RequestOption) (sounds []*SoundboardSound, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointSoundboardDefaultSounds, nil, EndpointSoundboardDefaultSounds, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &sounds)
	return
}

// GuildSoundboardSounds returns all soundboard sounds for the given guild.
// guildID : The ID of a Guild.
func (s *Session) GuildSoundboardSounds(guildID string, options ...RequestOption) (sounds []*SoundboardSound, err error) {
	endpoint := EndpointGuildSoundboardSounds(guildID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	var temp struct {
		Items []*SoundboardSound `json:"items"`
	}
	err = unmarshal(body, &temp)
	if err != nil {
		return
	}

	sounds = temp.Items
	return
}

// GuildSoundboardSound returns the specified guild soundboard sound.
// guildID : The ID of a Guild.
// soundID : The ID of a Soundboard Sound.
func (s *Session) GuildSoundboardSound(guildID, soundID string, options ...RequestOption) (sound *SoundboardSound, err error) {
	endpoint := EndpointGuildSoundboardSound(guildID, soundID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &sound)
	return
}

// GuildSoundboardSoundCreate creates a new soundboard sound.
// guildID : The ID of a Guild.
// data    : New soundboard sound data.
func (s *Session) GuildSoundboardSoundCreate(guildID string, data *SoundboardSoundParams, options ...RequestOption) (sound *SoundboardSound, err error) {
	if data == nil {
		return nil, fmt.Errorf("soundboard sound data cannot be nil")
	}

	endpoint := EndpointGuildSoundboardSounds(guildID)
	body, err := s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &sound)
	return
}

// GuildSoundboardSoundEdit modifies and returns the updated guild soundboard sound.
// guildID : The ID of a Guild.
// soundID : The ID of a Soundboard Sound.
// data    : Updated soundboard sound data.
func (s *Session) GuildSoundboardSoundEdit(guildID, soundID string, data *SoundboardSoundParams, options ...RequestOption) (sound *SoundboardSound, err error) {
	if data == nil {
		return nil, fmt.Errorf("soundboard sound data cannot be nil")
	}

	endpoint := EndpointGuildSoundboardSound(guildID, soundID)
	body, err := s.RequestWithBucketID("PATCH", endpoint, data, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &sound)
	return
}

// GuildSoundboardSoundDelete deletes a soundboard sound.
// guildID : The ID of a Guild.
// soundID : The ID of a Soundboard Sound.
func (s *Session) GuildSoundboardSoundDelete(guildID, soundID string, options ...RequestOption) (err error) {
	endpoint := EndpointGuildSoundboardSound(guildID, soundID)
	_, err = s.RequestWithBucketID("DELETE", endpoint, nil, endpoint, options...)
	return
}

// CurrentApplication returns the application associated with the current bot user.
func (s *Session) CurrentApplication(options ...RequestOption) (application *Application, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointCurrentApplication, nil, EndpointCurrentApplication, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &application)
	return
}

// CurrentApplicationEdit updates and returns the application associated with the current bot user.
func (s *Session) CurrentApplicationEdit(params *ApplicationEditParams, options ...RequestOption) (application *Application, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointCurrentApplication, params, EndpointCurrentApplication, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &application)
	return
}

// ApplicationGet returns the application with the given ID.
func (s *Session) ApplicationGet(appID string, options ...RequestOption) (application *Application, err error) {
	endpoint := EndpointApplication(appID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &application); err != nil {
		return nil, err
	}
	if application == nil {
		return nil, fmt.Errorf("%w: application response is null", ErrJSONUnmarshal)
	}
	return application, nil
}

// ApplicationEdit updates and returns the application with the given ID.
func (s *Session) ApplicationEdit(appID string, params *ApplicationEditParams, options ...RequestOption) (application *Application, err error) {
	endpoint := EndpointApplication(appID)
	body, err := s.RequestWithBucketID("PATCH", endpoint, params, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &application); err != nil {
		return nil, err
	}
	if application == nil {
		return nil, fmt.Errorf("%w: application response is null", ErrJSONUnmarshal)
	}
	return application, nil
}

// ApplicationAttachmentUpload uploads an attachment for the given application.
func (s *Session) ApplicationAttachmentUpload(appID string, file *File, options ...RequestOption) (response *ActivitiesAttachmentResponse, err error) {
	if file == nil {
		return nil, fmt.Errorf("application attachment file cannot be nil")
	}

	upload := *file
	upload.FieldName = "file"
	contentType, requestBody, err := multipartBody(nil, nil, []*File{&upload}, false)
	if err != nil {
		return nil, err
	}

	endpoint := EndpointApplicationAttachment(appID)
	body, err := s.RequestRaw("POST", endpoint, contentType, requestBody, endpoint, 0, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &response); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("%w: application attachment response is null", ErrJSONUnmarshal)
	}
	if response.Attachment == nil {
		return nil, fmt.Errorf("%w: application attachment response is missing attachment", ErrJSONUnmarshal)
	}

	return response, nil
}

// ApplicationEmojis returns all emojis for the given application
// appID : ID of the application
func (s *Session) ApplicationEmojis(appID string, options ...RequestOption) (emojis []*Emoji, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointApplicationEmojis(appID), nil, EndpointApplicationEmojis(appID), options...)
	if err != nil {
		return
	}

	var temp struct {
		Items []*Emoji `json:"items"`
	}

	err = unmarshal(body, &temp)
	if err != nil {
		return
	}

	emojis = temp.Items
	return
}

// ApplicationEmoji returns the emoji for the given application.
// appID   : ID of the application
// emojiID : ID of an Emoji to retrieve
func (s *Session) ApplicationEmoji(appID, emojiID string, options ...RequestOption) (emoji *Emoji, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointApplicationEmoji(appID, emojiID), nil, EndpointApplicationEmoji(appID, emojiID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// ApplicationEmojiCreate creates a new Emoji for the given application.
// appID : ID of the application
// data  : New Emoji data
func (s *Session) ApplicationEmojiCreate(appID string, data *EmojiParams, options ...RequestOption) (emoji *Emoji, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointApplicationEmojis(appID), data, EndpointApplicationEmojis(appID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// ApplicationEmojiEdit modifies and returns updated Emoji for the given application.
// appID   : ID of the application
// emojiID : ID of an Emoji
// data    : Updated Emoji data
func (s *Session) ApplicationEmojiEdit(appID string, emojiID string, data *EmojiParams, options ...RequestOption) (emoji *Emoji, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointApplicationEmoji(appID, emojiID), data, EndpointApplicationEmojis(appID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &emoji)
	return
}

// ApplicationEmojiDelete deletes an Emoji for the given application.
// appID   : ID of the application
// emojiID : ID of an Emoji
func (s *Session) ApplicationEmojiDelete(appID, emojiID string, options ...RequestOption) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointApplicationEmoji(appID, emojiID), nil, EndpointApplicationEmojis(appID), options...)
	return
}

// ApplicationActivityInstance returns a serialized activity instance, if it exists.
// appID      : ID of the application.
// instanceID : ID of the activity instance.
func (s *Session) ApplicationActivityInstance(appID, instanceID string, options ...RequestOption) (st *ApplicationActivityInstance, err error) {
	endpoint := EndpointApplicationActivityInstance(appID, instanceID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildTemplate returns a GuildTemplate for the given code
// templateCode: The Code of a GuildTemplate
func (s *Session) GuildTemplate(templateCode string, options ...RequestOption) (st *GuildTemplate, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildTemplate(templateCode), nil, EndpointGuildTemplate(templateCode), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildCreateWithTemplate creates a guild based on a GuildTemplate
// templateCode: The Code of a GuildTemplate
// name: The name of the guild (2-100) characters
// icon: base64 encoded 128x128 image for the guild icon
func (s *Session) GuildCreateWithTemplate(templateCode, name, icon string, options ...RequestOption) (st *Guild, err error) {

	data := struct {
		Name string `json:"name"`
		Icon string `json:"icon"`
	}{name, icon}

	body, err := s.RequestWithBucketID("POST", EndpointGuildTemplate(templateCode), data, EndpointGuildTemplate(templateCode), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildTemplates returns all of GuildTemplates
// guildID: The ID of the guild
func (s *Session) GuildTemplates(guildID string, options ...RequestOption) (st []*GuildTemplate, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildTemplates(guildID), nil, EndpointGuildTemplates(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildTemplateCreate creates a template for the guild
// guildID : The ID of the guild
// data    : Template metadata
//
// Deprecated: use GuildTemplateCreateWithError to receive request and response errors.
func (s *Session) GuildTemplateCreate(guildID string, data *GuildTemplateParams, options ...RequestOption) *GuildTemplate {
	st, err := s.GuildTemplateCreateWithError(guildID, data, options...)
	if err != nil {
		return nil
	}
	return st
}

// GuildTemplateCreateWithError creates a template for the guild and returns request and response errors.
// guildID : The ID of the guild
// data    : Template metadata
func (s *Session) GuildTemplateCreateWithError(guildID string, data *GuildTemplateParams, options ...RequestOption) (st *GuildTemplate, err error) {
	endpoint := EndpointGuildTemplates(guildID)
	body, err := s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &st); err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fmt.Errorf("%w: guild template create response is null", ErrJSONUnmarshal)
	}
	return st, nil
}

// GuildTemplateSync syncs the template to the guild's current state
// guildID: The ID of the guild
// templateCode: The code of the template
func (s *Session) GuildTemplateSync(guildID, templateCode string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("PUT", EndpointGuildTemplateSync(guildID, templateCode), nil, EndpointGuildTemplateSync(guildID, ""), options...)
	return
}

// GuildTemplateEdit modifies the template's metadata
// guildID      : The ID of the guild
// templateCode : The code of the template
// data         : New template metadata
func (s *Session) GuildTemplateEdit(guildID, templateCode string, data *GuildTemplateParams, options ...RequestOption) (st *GuildTemplate, err error) {

	body, err := s.RequestWithBucketID("PATCH", EndpointGuildTemplateSync(guildID, templateCode), data, EndpointGuildTemplateSync(guildID, ""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildTemplateDelete deletes the template
// guildID: The ID of the guild
// templateCode: The code of the template
func (s *Session) GuildTemplateDelete(guildID, templateCode string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointGuildTemplateSync(guildID, templateCode), nil, EndpointGuildTemplateSync(guildID, ""), options...)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Channels
// ------------------------------------------------------------------------------------------------

// Channel returns a Channel structure of a specific Channel.
// channelID  : The ID of the Channel you want returned.
func (s *Session) Channel(channelID string, options ...RequestOption) (st *Channel, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointChannel(channelID), nil, EndpointChannel(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelEdit edits the given channel and returns the updated Channel data.
// channelID  : The ID of a Channel.
// data       : New Channel data.
func (s *Session) ChannelEdit(channelID string, data *ChannelEdit, options ...RequestOption) (st *Channel, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointChannel(channelID), data, EndpointChannel(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return

}

// ChannelEditComplex edits an existing channel, replacing the parameters entirely with ChannelEdit struct
// NOTE: deprecated, use ChannelEdit instead
// channelID     : The ID of a Channel
// data          : The channel struct to send
func (s *Session) ChannelEditComplex(channelID string, data *ChannelEdit, options ...RequestOption) (st *Channel, err error) {
	return s.ChannelEdit(channelID, data, options...)
}

// ChannelDelete deletes the given channel
// channelID  : The ID of a Channel
func (s *Session) ChannelDelete(channelID string, options ...RequestOption) (st *Channel, err error) {

	body, err := s.RequestWithBucketID("DELETE", EndpointChannel(channelID), nil, EndpointChannel(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelTyping broadcasts to all members that authenticated user is typing in
// the given channel.
// channelID  : The ID of a Channel
func (s *Session) ChannelTyping(channelID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("POST", EndpointChannelTyping(channelID), nil, EndpointChannelTyping(channelID), options...)
	return
}

// ChannelMessages returns an array of Message structures for messages within
// a given channel.
// channelID : The ID of a Channel.
// limit     : The number messages that can be returned. (max 100)
// beforeID  : If provided all messages returned will be before given ID.
// afterID   : If provided all messages returned will be after given ID.
// aroundID  : If provided all messages returned will be around given ID.
func (s *Session) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...RequestOption) (st []*Message, err error) {

	uri := EndpointChannelMessages(channelID)

	v := url.Values{}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if afterID != "" {
		v.Set("after", afterID)
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}
	if aroundID != "" {
		v.Set("around", aroundID)
	}
	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointChannelMessages(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelMessage gets a single message by ID from a given channel.
// channeld  : The ID of a Channel
// messageID : the ID of a Message
func (s *Session) ChannelMessage(channelID, messageID string, options ...RequestOption) (st *Message, err error) {

	response, err := s.RequestWithBucketID("GET", EndpointChannelMessage(channelID, messageID), nil, EndpointChannelMessage(channelID, ""), options...)
	if err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

// ChannelMessageSend sends a message to the given channel.
// channelID : The ID of a Channel.
// content   : The message to send.
func (s *Session) ChannelMessageSend(channelID string, content string, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Content: content,
	}, options...)
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "%0D", "\n", "%0A")

func setEmbedTypes(embeds []*MessageEmbed) error {
	for _, embed := range embeds {
		if embed == nil {
			return fmt.Errorf("message embed cannot be nil")
		}
		if embed.Type == "" {
			embed.Type = "rich"
		}
	}
	return nil
}

func (s *Session) applyAllowedMentionsToMessage(data *MessageSend) {
	if data.AllowedMentions == nil && s.AllowedMentions != nil {
		data.AllowedMentions = s.AllowedMentions
	}
}

func (s *Session) applyAllowedMentionsToMessageEdit(data *MessageEdit) {
	if data.AllowedMentions == nil && s.AllowedMentions != nil {
		data.AllowedMentions = s.AllowedMentions
	}
}

func (s *Session) applyAllowedMentionsToWebhook(data *WebhookParams) {
	if data.AllowedMentions == nil && s.AllowedMentions != nil {
		data.AllowedMentions = s.AllowedMentions
	}
}

func (s *Session) applyAllowedMentionsToWebhookEdit(data *WebhookEdit) {
	if data.AllowedMentions == nil && s.AllowedMentions != nil {
		data.AllowedMentions = s.AllowedMentions
	}
}

func (s *Session) applyAllowedMentionsToInteractionResponse(data *InteractionResponseData) {
	if data.AllowedMentions == nil && s.AllowedMentions != nil {
		data.AllowedMentions = s.AllowedMentions
	}
}

// ChannelMessageSendComplex sends a message to the given channel.
// channelID : The ID of a Channel.
// data      : The message struct to send.
func (s *Session) ChannelMessageSendComplex(channelID string, data *MessageSend, options ...RequestOption) (st *Message, err error) {
	if data == nil {
		return nil, fmt.Errorf("message send data cannot be nil")
	}

	// TODO: Remove this when compatibility is not required.
	if data.Embed != nil {
		if data.Embeds == nil {
			data.Embeds = []*MessageEmbed{data.Embed}
		} else {
			err = fmt.Errorf("cannot specify both Embed and Embeds")
			return
		}
	}

	if err = setEmbedTypes(data.Embeds); err != nil {
		return
	}
	s.applyAllowedMentionsToMessage(data)
	endpoint := EndpointChannelMessages(channelID)

	// TODO: Remove this when compatibility is not required.
	files := data.Files
	if data.File != nil {
		if files == nil {
			files = []*File{data.File}
		} else {
			err = fmt.Errorf("cannot specify both File and Files")
			return
		}
	}

	if data.StickerIDs != nil {
		if len(data.StickerIDs) > 3 {
			err = fmt.Errorf("cannot send more than 3 stickers")
			return
		}
	}

	var response []byte
	if len(files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(data, files)
		if encodeErr != nil {
			return st, encodeErr
		}
		response, err = s.RequestRaw("POST", endpoint, contentType, body, endpoint, 0, options...)
	} else {
		response, err = s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	}
	if err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

// ChannelMessageSendTTS sends a message to the given channel with Text to Speech.
// channelID : The ID of a Channel.
// content   : The message to send.
func (s *Session) ChannelMessageSendTTS(channelID string, content string, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Content: content,
		TTS:     true,
	}, options...)
}

// ChannelMessageSendEmbed sends a message to the given channel with embedded data.
// channelID : The ID of a Channel.
// embed     : The embed data to send.
func (s *Session) ChannelMessageSendEmbed(channelID string, embed *MessageEmbed, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageSendEmbeds(channelID, []*MessageEmbed{embed}, options...)
}

// ChannelMessageSendEmbeds sends a message to the given channel with multiple embedded data.
// channelID : The ID of a Channel.
// embeds    : The embeds data to send.
func (s *Session) ChannelMessageSendEmbeds(channelID string, embeds []*MessageEmbed, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Embeds: embeds,
	}, options...)
}

// ChannelMessageSendReply sends a message to the given channel with reference data.
// channelID : The ID of a Channel.
// content   : The message to send.
// reference : The message reference to send.
func (s *Session) ChannelMessageSendReply(channelID string, content string, reference *MessageReference, options ...RequestOption) (*Message, error) {
	if reference == nil {
		return nil, fmt.Errorf("reply attempted with nil message reference")
	}
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Content:   content,
		Reference: reference,
	}, options...)
}

// ChannelMessageSendEmbedReply sends a message to the given channel with reference data and embedded data.
// channelID : The ID of a Channel.
// embed   : The embed data to send.
// reference : The message reference to send.
func (s *Session) ChannelMessageSendEmbedReply(channelID string, embed *MessageEmbed, reference *MessageReference, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageSendEmbedsReply(channelID, []*MessageEmbed{embed}, reference, options...)
}

// ChannelMessageSendEmbedsReply sends a message to the given channel with reference data and multiple embedded data.
// channelID : The ID of a Channel.
// embeds    : The embeds data to send.
// reference : The message reference to send.
func (s *Session) ChannelMessageSendEmbedsReply(channelID string, embeds []*MessageEmbed, reference *MessageReference, options ...RequestOption) (*Message, error) {
	if reference == nil {
		return nil, fmt.Errorf("reply attempted with nil message reference")
	}
	return s.ChannelMessageSendComplex(channelID, &MessageSend{
		Embeds:    embeds,
		Reference: reference,
	}, options...)
}

// ChannelMessageEdit edits an existing message, replacing it entirely with
// the given content.
// channelID  : The ID of a Channel
// messageID  : The ID of a Message
// content    : The contents of the message
func (s *Session) ChannelMessageEdit(channelID, messageID, content string, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageEditComplex(NewMessageEdit(channelID, messageID).SetContent(content), options...)
}

// ChannelMessageEditComplex edits an existing message, replacing it entirely with
// the given MessageEdit struct
func (s *Session) ChannelMessageEditComplex(m *MessageEdit, options ...RequestOption) (st *Message, err error) {
	if m == nil {
		return nil, fmt.Errorf("message edit data cannot be nil")
	}

	// TODO: Remove this when compatibility is not required.
	if m.Embed != nil {
		if m.Embeds == nil {
			m.Embeds = &[]*MessageEmbed{m.Embed}
		} else {
			err = fmt.Errorf("cannot specify both Embed and Embeds")
			return
		}
	}

	if m.Embeds != nil {
		if err = setEmbedTypes(*m.Embeds); err != nil {
			return
		}
	}
	s.applyAllowedMentionsToMessageEdit(m)

	endpoint := EndpointChannelMessage(m.Channel, m.ID)

	var response []byte
	if len(m.Files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(m, m.Files)
		if encodeErr != nil {
			return st, encodeErr
		}
		response, err = s.RequestRaw("PATCH", endpoint, contentType, body, EndpointChannelMessage(m.Channel, ""), 0, options...)
	} else {
		response, err = s.RequestWithBucketID("PATCH", endpoint, m, EndpointChannelMessage(m.Channel, ""), options...)
	}
	if err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

// ChannelMessageEditEmbed edits an existing message with embedded data.
// channelID : The ID of a Channel
// messageID : The ID of a Message
// embed     : The embed data to send
func (s *Session) ChannelMessageEditEmbed(channelID, messageID string, embed *MessageEmbed, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageEditEmbeds(channelID, messageID, []*MessageEmbed{embed}, options...)
}

// ChannelMessageEditEmbeds edits an existing message with multiple embedded data.
// channelID : The ID of a Channel
// messageID : The ID of a Message
// embeds    : The embeds data to send
func (s *Session) ChannelMessageEditEmbeds(channelID, messageID string, embeds []*MessageEmbed, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageEditComplex(NewMessageEdit(channelID, messageID).SetEmbeds(embeds), options...)
}

// ChannelMessageDelete deletes a message from the Channel.
func (s *Session) ChannelMessageDelete(channelID, messageID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointChannelMessage(channelID, messageID), nil, EndpointChannelMessage(channelID, ""), options...)
	return
}

// ChannelMessagesBulkDelete bulk deletes the messages from the channel for the provided messageIDs.
// If only one messageID is in the slice call channelMessageDelete function.
// If the slice is empty do nothing.
// channelID : The ID of the channel for the messages to delete.
// messages  : The IDs of the messages to be deleted. A slice of string IDs. A maximum of 100 messages.
func (s *Session) ChannelMessagesBulkDelete(channelID string, messages []string, options ...RequestOption) (err error) {

	if len(messages) == 0 {
		return
	}

	if len(messages) == 1 {
		err = s.ChannelMessageDelete(channelID, messages[0], options...)
		return
	}

	if len(messages) > 100 {
		messages = messages[:100]
	}

	data := struct {
		Messages []string `json:"messages"`
	}{messages}

	_, err = s.RequestWithBucketID("POST", EndpointChannelMessagesBulkDelete(channelID), data, EndpointChannelMessagesBulkDelete(channelID), options...)
	return
}

// ChannelMessagePin pins a message within a given channel.
// channelID: The ID of a channel.
// messageID: The ID of a message.
func (s *Session) ChannelMessagePin(channelID, messageID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("PUT", EndpointChannelMessagePin(channelID, messageID), nil, EndpointChannelMessagePin(channelID, ""), options...)
	return
}

// ChannelMessageUnpin unpins a message within a given channel.
// channelID: The ID of a channel.
// messageID: The ID of a message.
func (s *Session) ChannelMessageUnpin(channelID, messageID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointChannelMessagePin(channelID, messageID), nil, EndpointChannelMessagePin(channelID, ""), options...)
	return
}

// ChannelMessagesPinned returns an array of Message structures for pinned messages
// within a given channel
// channelID : The ID of a Channel.
// before : If specified returns only pinned messages before the timestamp
// limit  : Optional maximum amount of pinned messages to return.
func (s *Session) ChannelMessagesPinned(channelID string, before *time.Time, limit int, options ...RequestOption) (pinnedMessages *ChannelMessagePinsList, err error) {
	uri := EndpointChannelMessagesPins(channelID)

	v := url.Values{}

	if before != nil {
		v.Set("before", before.Format(time.RFC3339))
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointChannelMessagesPins(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &pinnedMessages)
	return
}

// ChannelFileSend sends a file to the given channel.
// channelID : The ID of a Channel.
// name: The name of the file.
// io.Reader : A reader for the file contents.
func (s *Session) ChannelFileSend(channelID, name string, r io.Reader, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{File: &File{Name: name, Reader: r}}, options...)
}

// ChannelFileSendWithMessage sends a file to the given channel with an message.
// DEPRECATED. Use ChannelMessageSendComplex instead.
// channelID : The ID of a Channel.
// content: Optional Message content.
// name: The name of the file.
// io.Reader : A reader for the file contents.
func (s *Session) ChannelFileSendWithMessage(channelID, content string, name string, r io.Reader, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, &MessageSend{File: &File{Name: name, Reader: r}, Content: content}, options...)
}

// ChannelInvites returns an array of Invite structures for the given channel
// channelID   : The ID of a Channel
func (s *Session) ChannelInvites(channelID string, options ...RequestOption) (st []*Invite, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointChannelInvites(channelID), nil, EndpointChannelInvites(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelInviteCreate creates a new invite for the given channel.
// channelID   : The ID of a Channel
// i           : An Invite struct with the values MaxAge, MaxUses and Temporary defined.
func (s *Session) ChannelInviteCreate(channelID string, i Invite, options ...RequestOption) (st *Invite, err error) {

	data := struct {
		MaxAge              int              `json:"max_age"`
		MaxUses             int              `json:"max_uses"`
		Temporary           bool             `json:"temporary"`
		Unique              bool             `json:"unique"`
		TargetType          InviteTargetType `json:"target_type,omitempty"`
		TargetUserID        string           `json:"target_user_id,omitempty"`
		TargetApplicationID string           `json:"target_application_id,omitempty"`
		RoleIDs             []string         `json:"role_ids,omitempty"`
	}{
		MaxAge:              i.MaxAge,
		MaxUses:             i.MaxUses,
		Temporary:           i.Temporary,
		Unique:              i.Unique,
		TargetType:          i.TargetType,
		TargetUserID:        i.TargetUserID,
		TargetApplicationID: i.TargetApplicationID,
		RoleIDs:             i.RoleIDs,
	}

	endpoint := EndpointChannelInvites(channelID)
	var body []byte
	if i.TargetUsersFile != nil {
		file := *i.TargetUsersFile
		file.FieldName = "target_users_file"

		contentType, requestBody, encodeErr := MultipartBodyWithJSON(data, []*File{&file})
		if encodeErr != nil {
			err = encodeErr
			return
		}
		body, err = s.RequestRaw("POST", endpoint, contentType, requestBody, endpoint, 0, options...)
	} else {
		body, err = s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	}
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelPermissionSet creates a Permission Override for the given channel.
// NOTE: This func name may changed.  Using Set instead of Create because
// you can both create a new override or update an override with this function.
func (s *Session) ChannelPermissionSet(channelID, targetID string, targetType PermissionOverwriteType, allow, deny int64, options ...RequestOption) (err error) {

	data := struct {
		ID    string                  `json:"id"`
		Type  PermissionOverwriteType `json:"type"`
		Allow int64                   `json:"allow,string"`
		Deny  int64                   `json:"deny,string"`
	}{targetID, targetType, allow, deny}

	_, err = s.RequestWithBucketID("PUT", EndpointChannelPermission(channelID, targetID), data, EndpointChannelPermission(channelID, ""), options...)
	return
}

// ChannelPermissionDelete deletes a specific permission override for the given channel.
// NOTE: Name of this func may change.
func (s *Session) ChannelPermissionDelete(channelID, targetID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointChannelPermission(channelID, targetID), nil, EndpointChannelPermission(channelID, ""), options...)
	return
}

// ChannelMessageCrosspost cross posts a message in a news channel to followers
// of the channel
// channelID   : The ID of a Channel
// messageID   : The ID of a Message
func (s *Session) ChannelMessageCrosspost(channelID, messageID string, options ...RequestOption) (st *Message, err error) {

	endpoint := EndpointChannelMessageCrosspost(channelID, messageID)

	body, err := s.RequestWithBucketID("POST", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelNewsFollow follows a news channel in the targetID
// channelID   : The ID of a News Channel
// targetID    : The ID of a Channel where the News Channel should post to
func (s *Session) ChannelNewsFollow(channelID, targetID string, options ...RequestOption) (st *ChannelFollow, err error) {

	endpoint := EndpointChannelFollow(channelID)

	data := struct {
		WebhookChannelID string `json:"webhook_channel_id"`
	}{targetID}

	body, err := s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Invites
// ------------------------------------------------------------------------------------------------

// Invite returns an Invite structure of the given invite
// inviteID : The invite code
func (s *Session) Invite(inviteID string, options ...RequestOption) (st *Invite, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointInvite(inviteID), nil, EndpointInvite(""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteWithCounts returns an Invite structure of the given invite including approximate member counts
// inviteID : The invite code
func (s *Session) InviteWithCounts(inviteID string, options ...RequestOption) (st *Invite, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointInvite(inviteID)+"?with_counts=true", nil, EndpointInvite(""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteComplex returns an Invite structure of the given invite including specified fields.
// inviteID                  : The invite code
// guildScheduledEventID     : If specified, includes specified guild scheduled event.
// withCounts                : Whether to include approximate member counts or not
// withExpiration            : Whether to include expiration time or not
func (s *Session) InviteComplex(inviteID, guildScheduledEventID string, withCounts, withExpiration bool, options ...RequestOption) (st *Invite, err error) {
	endpoint := EndpointInvite(inviteID)
	v := url.Values{}
	if guildScheduledEventID != "" {
		v.Set("guild_scheduled_event_id", guildScheduledEventID)
	}
	if withCounts {
		v.Set("with_counts", "true")
	}
	if withExpiration {
		v.Set("with_expiration", "true")
	}

	if len(v) != 0 {
		endpoint += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", endpoint, nil, EndpointInvite(""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteDelete deletes an existing invite
// inviteID   : the code of an invite
func (s *Session) InviteDelete(inviteID string, options ...RequestOption) (st *Invite, err error) {

	body, err := s.RequestWithBucketID("DELETE", EndpointInvite(inviteID), nil, EndpointInvite(""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteAccept accepts an Invite to a Guild or Channel
// inviteID : The invite code
func (s *Session) InviteAccept(inviteID string, options ...RequestOption) (st *Invite, err error) {

	body, err := s.RequestWithBucketID("POST", EndpointInvite(inviteID), nil, EndpointInvite(""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// InviteTargetUsers returns the CSV target-user file for the given invite.
// inviteID : The invite code.
func (s *Session) InviteTargetUsers(inviteID string, options ...RequestOption) (st []byte, err error) {
	endpoint := EndpointInviteTargetUsers(inviteID)
	st, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	return
}

// InviteTargetUsersUpdate updates the CSV target-user file for the given invite.
// inviteID : The invite code.
// file     : CSV file with a single user_id column.
func (s *Session) InviteTargetUsersUpdate(inviteID string, file *File, options ...RequestOption) (err error) {
	if file == nil {
		return fmt.Errorf("target users file cannot be nil")
	}

	upload := *file
	upload.FieldName = "target_users_file"

	contentType, body, err := multipartBody(nil, nil, []*File{&upload}, false)
	if err != nil {
		return
	}

	endpoint := EndpointInviteTargetUsers(inviteID)
	_, err = s.RequestRaw("PUT", endpoint, contentType, body, endpoint, 0, options...)
	return
}

// InviteTargetUsersJobStatus returns the target-users file processing status for the given invite.
// inviteID : The invite code.
func (s *Session) InviteTargetUsersJobStatus(inviteID string, options ...RequestOption) (st *InviteTargetUsersJob, err error) {
	endpoint := EndpointInviteTargetUsersJobStatus(inviteID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ChannelVoiceStatusSet sets or clears a voice channel status.
// channelID : The ID of a voice Channel.
// status    : New status text, or nil to clear the status.
func (s *Session) ChannelVoiceStatusSet(channelID string, status *string, options ...RequestOption) (err error) {
	endpoint := EndpointChannelVoiceStatus(channelID)
	data := struct {
		Status *string `json:"status"`
	}{status}

	_, err = s.RequestWithBucketID("PUT", endpoint, data, endpoint, options...)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Voice
// ------------------------------------------------------------------------------------------------

// VoiceRegions returns the voice server regions
func (s *Session) VoiceRegions(options ...RequestOption) (st []*VoiceRegion, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointVoiceRegions, nil, EndpointVoiceRegions, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to Discord Websockets
// ------------------------------------------------------------------------------------------------

// Gateway returns the websocket Gateway address
func (s *Session) Gateway(options ...RequestOption) (gateway string, err error) {

	response, err := s.RequestWithBucketID("GET", EndpointGateway, nil, EndpointGateway, options...)
	if err != nil {
		return
	}

	temp := struct {
		URL string `json:"url"`
	}{}

	err = unmarshal(response, &temp)
	if err != nil {
		return
	}

	gateway = temp.URL

	// Ensure the gateway always has a trailing slash.
	// MacOS will fail to connect if we add query params without a trailing slash on the base domain.
	if !strings.HasSuffix(gateway, "/") {
		gateway += "/"
	}

	return
}

// GatewayBot returns the websocket Gateway address and the recommended number of shards
func (s *Session) GatewayBot(options ...RequestOption) (st *GatewayBotResponse, err error) {

	response, err := s.RequestWithBucketID("GET", EndpointGatewayBot, nil, EndpointGatewayBot, options...)
	if err != nil {
		return
	}

	err = unmarshal(response, &st)
	if err != nil {
		return
	}

	// Ensure the gateway always has a trailing slash.
	// MacOS will fail to connect if we add query params without a trailing slash on the base domain.
	if !strings.HasSuffix(st.URL, "/") {
		st.URL += "/"
	}

	return
}

// Functions specific to Webhooks

// WebhookCreate returns a new Webhook.
// channelID: The ID of a Channel.
// name     : The name of the webhook.
// avatar   : The avatar of the webhook.
func (s *Session) WebhookCreate(channelID, name, avatar string, options ...RequestOption) (st *Webhook, err error) {

	data := struct {
		Name   string `json:"name"`
		Avatar string `json:"avatar,omitempty"`
	}{name, avatar}

	body, err := s.RequestWithBucketID("POST", EndpointChannelWebhooks(channelID), data, EndpointChannelWebhooks(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// ChannelWebhooks returns all webhooks for a given channel.
// channelID: The ID of a channel.
func (s *Session) ChannelWebhooks(channelID string, options ...RequestOption) (st []*Webhook, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointChannelWebhooks(channelID), nil, EndpointChannelWebhooks(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// GuildWebhooks returns all webhooks for a given guild.
// guildID: The ID of a Guild.
func (s *Session) GuildWebhooks(guildID string, options ...RequestOption) (st []*Webhook, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointGuildWebhooks(guildID), nil, EndpointGuildWebhooks(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// Webhook returns a webhook for a given ID
// webhookID: The ID of a webhook.
func (s *Session) Webhook(webhookID string, options ...RequestOption) (st *Webhook, err error) {

	body, err := s.RequestWithBucketID("GET", EndpointWebhook(webhookID), nil, EndpointWebhooks, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// WebhookWithToken returns a webhook for a given ID
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook.
func (s *Session) WebhookWithToken(webhookID, token string, options ...RequestOption) (st *Webhook, err error) {

	options = withoutAuthorizationOptions(options)
	body, err := s.requestWithBucketIDNoGlobal("GET", EndpointWebhookToken(webhookID, token), nil, webhookTokenBucketID(webhookID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// WebhookEdit updates an existing Webhook.
// webhookID: The ID of a webhook.
// name     : The name of the webhook.
// avatar   : The avatar of the webhook.
func (s *Session) WebhookEdit(webhookID, name, avatar, channelID string, options ...RequestOption) (st *Webhook, err error) {

	data := struct {
		Name      string `json:"name,omitempty"`
		Avatar    string `json:"avatar,omitempty"`
		ChannelID string `json:"channel_id,omitempty"`
	}{name, avatar, channelID}

	body, err := s.RequestWithBucketID("PATCH", EndpointWebhook(webhookID), data, EndpointWebhooks, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// WebhookEditWithToken updates an existing Webhook with an auth token.
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook.
// name     : The name of the webhook.
// avatar   : The avatar of the webhook.
func (s *Session) WebhookEditWithToken(webhookID, token, name, avatar string, options ...RequestOption) (st *Webhook, err error) {

	data := struct {
		Name   string `json:"name,omitempty"`
		Avatar string `json:"avatar,omitempty"`
	}{name, avatar}

	var body []byte
	options = withoutAuthorizationOptions(options)
	body, err = s.requestWithBucketIDNoGlobal("PATCH", EndpointWebhookToken(webhookID, token), data, webhookTokenBucketID(webhookID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)

	return
}

// WebhookDelete deletes a webhook for a given ID
// webhookID: The ID of a webhook.
func (s *Session) WebhookDelete(webhookID string, options ...RequestOption) (err error) {

	_, err = s.RequestWithBucketID("DELETE", EndpointWebhook(webhookID), nil, EndpointWebhooks, options...)

	return
}

// WebhookDeleteWithToken deletes a webhook for a given ID with an auth token.
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook.
func (s *Session) WebhookDeleteWithToken(webhookID, token string, options ...RequestOption) (st *Webhook, err error) {

	options = withoutAuthorizationOptions(options)
	body, err := s.requestWithBucketIDNoGlobal("DELETE", EndpointWebhookToken(webhookID, token), nil, webhookTokenBucketID(webhookID), options...)
	if err != nil {
		return
	}

	if len(body) == 0 {
		return nil, nil
	}

	err = unmarshal(body, &st)

	return
}

func (s *Session) webhookExecute(webhookID, token string, executeOptions WebhookExecuteOptions, data *WebhookParams, options ...RequestOption) (st *Message, err error) {
	if data == nil {
		return nil, fmt.Errorf("webhook data cannot be nil")
	}
	s.applyAllowedMentionsToWebhook(data)

	uri := EndpointWebhookToken(webhookID, token)
	options = withoutAuthorizationOptions(options)

	v := url.Values{}
	if executeOptions.Wait {
		v.Set("wait", "true")
	}
	if executeOptions.ThreadID != "" {
		v.Set("thread_id", executeOptions.ThreadID)
	}
	if executeOptions.WithComponents {
		v.Set("with_components", "true")
	}
	if len(v) != 0 {
		uri += "?" + v.Encode()
	}

	var response []byte
	bucketID := webhookTokenBucketID(webhookID)
	if len(data.Files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(data, data.Files)
		if encodeErr != nil {
			return st, encodeErr
		}

		response, err = s.requestRawWithBucketIDNoGlobal("POST", uri, contentType, body, bucketID, 0, options...)
	} else {
		response, err = s.requestWithBucketIDNoGlobal("POST", uri, data, bucketID, options...)
	}
	if !executeOptions.Wait || err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

func (s *Session) interactionWebhookExecute(appID, token string, wait bool, data *WebhookParams, options ...RequestOption) (st *Message, err error) {
	if data == nil {
		return nil, fmt.Errorf("webhook data cannot be nil")
	}
	s.applyAllowedMentionsToWebhook(data)

	uri := EndpointWebhookToken(appID, token)
	options = withoutAuthorizationOptions(options)

	if wait {
		v := url.Values{}
		v.Set("wait", "true")
		uri += "?" + v.Encode()
	}

	var response []byte
	bucketID := interactionWebhookTokenBucketID(appID, token)
	if len(data.Files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(data, data.Files)
		if encodeErr != nil {
			return st, encodeErr
		}

		response, err = s.requestRawWithBucketIDNoGlobalTTL("POST", uri, contentType, body, bucketID, 0, interactionWebhookBucketTTL, options...)
	} else {
		response, err = s.requestWithBucketIDNoGlobalTTL("POST", uri, data, bucketID, interactionWebhookBucketTTL, options...)
	}
	if !wait || err != nil {
		return
	}

	err = unmarshal(response, &st)
	return
}

// WebhookExecute executes a webhook.
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook
// wait     : Waits for server confirmation of message send and ensures that the return struct is populated (it is nil otherwise)
func (s *Session) WebhookExecute(webhookID, token string, wait bool, data *WebhookParams, options ...RequestOption) (st *Message, err error) {
	return s.webhookExecute(webhookID, token, WebhookExecuteOptions{Wait: wait}, data, options...)
}

// WebhookThreadExecute executes a webhook in a thread.
// webhookID: The ID of a webhook.
// token    : The auth token for the webhook
// wait     : Waits for server confirmation of message send and ensures that the return struct is populated (it is nil otherwise)
// threadID :	Sends a message to the specified thread within a webhook's channel. The thread will automatically be unarchived.
func (s *Session) WebhookThreadExecute(webhookID, token string, wait bool, threadID string, data *WebhookParams, options ...RequestOption) (st *Message, err error) {
	return s.webhookExecute(webhookID, token, WebhookExecuteOptions{Wait: wait, ThreadID: threadID}, data, options...)
}

// WebhookExecuteComplex executes a webhook with query-string options.
// webhookID     : The ID of a webhook.
// token         : The auth token for the webhook.
// executeOptions: Query parameters for the request.
// data          : Data of the message to send.
func (s *Session) WebhookExecuteComplex(webhookID, token string, executeOptions WebhookExecuteOptions, data *WebhookParams, options ...RequestOption) (st *Message, err error) {
	return s.webhookExecute(webhookID, token, executeOptions, data, options...)
}

// WebhookMessage gets a webhook message.
// webhookID : The ID of a webhook
// token     : The auth token for the webhook
// messageID : The ID of message to get
func (s *Session) WebhookMessage(webhookID, token, messageID string, options ...RequestOption) (message *Message, err error) {
	return s.WebhookMessageWithOptions(webhookID, token, messageID, WebhookMessageOptions{}, options...)
}

// WebhookMessageWithOptions gets a webhook message with query-string options.
func (s *Session) WebhookMessageWithOptions(webhookID, token, messageID string, messageOptions WebhookMessageOptions, options ...RequestOption) (message *Message, err error) {
	uri := webhookMessageURI(webhookID, token, messageID, messageOptions.ThreadID, false)

	options = withoutAuthorizationOptions(options)
	body, err := s.requestWithBucketIDNoGlobal("GET", uri, nil, webhookMessageBucketID(webhookID), options...)
	if err != nil {
		return
	}

	err = Unmarshal(body, &message)

	return
}

func (s *Session) interactionWebhookMessage(appID, token, messageID string, options ...RequestOption) (message *Message, err error) {
	uri := EndpointWebhookMessage(appID, token, messageID)
	bucketID := interactionWebhookMessageBucketID(appID, token)

	options = withoutAuthorizationOptions(options)
	body, err := s.requestWithBucketIDNoGlobalTTL("GET", uri, nil, bucketID, interactionWebhookBucketTTL, options...)
	if err != nil {
		return
	}

	err = Unmarshal(body, &message)

	return
}

// WebhookMessageEdit edits a webhook message and returns a new one.
// webhookID : The ID of a webhook
// token     : The auth token for the webhook
// messageID : The ID of message to edit
func (s *Session) WebhookMessageEdit(webhookID, token, messageID string, data *WebhookEdit, options ...RequestOption) (st *Message, err error) {
	return s.WebhookMessageEditWithOptions(webhookID, token, messageID, WebhookMessageEditOptions{}, data, options...)
}

// WebhookMessageEditWithOptions edits a webhook message with query-string options.
func (s *Session) WebhookMessageEditWithOptions(webhookID, token, messageID string, editOptions WebhookMessageEditOptions, data *WebhookEdit, options ...RequestOption) (st *Message, err error) {
	if data == nil {
		return nil, fmt.Errorf("webhook edit data cannot be nil")
	}
	s.applyAllowedMentionsToWebhookEdit(data)

	uri := webhookMessageURI(webhookID, token, messageID, editOptions.ThreadID, editOptions.WithComponents)
	bucketID := webhookMessageBucketID(webhookID)
	options = withoutAuthorizationOptions(options)

	var response []byte
	if len(data.Files) > 0 {
		contentType, body, err := MultipartBodyWithJSON(data, data.Files)
		if err != nil {
			return nil, err
		}

		response, err = s.requestRawWithBucketIDNoGlobal("PATCH", uri, contentType, body, bucketID, 0, options...)
		if err != nil {
			return nil, err
		}
	} else {
		response, err = s.requestWithBucketIDNoGlobal("PATCH", uri, data, bucketID, options...)

		if err != nil {
			return nil, err
		}
	}

	err = unmarshal(response, &st)
	return
}

func (s *Session) interactionWebhookMessageEdit(appID, token, messageID string, data *WebhookEdit, options ...RequestOption) (st *Message, err error) {
	if data == nil {
		return nil, fmt.Errorf("webhook edit data cannot be nil")
	}
	s.applyAllowedMentionsToWebhookEdit(data)

	uri := EndpointWebhookMessage(appID, token, messageID)
	bucketID := interactionWebhookMessageBucketID(appID, token)
	options = withoutAuthorizationOptions(options)

	var response []byte
	if len(data.Files) > 0 {
		contentType, body, err := MultipartBodyWithJSON(data, data.Files)
		if err != nil {
			return nil, err
		}

		response, err = s.requestRawWithBucketIDNoGlobalTTL("PATCH", uri, contentType, body, bucketID, 0, interactionWebhookBucketTTL, options...)
		if err != nil {
			return nil, err
		}
	} else {
		response, err = s.requestWithBucketIDNoGlobalTTL("PATCH", uri, data, bucketID, interactionWebhookBucketTTL, options...)

		if err != nil {
			return nil, err
		}
	}

	err = unmarshal(response, &st)
	return
}

// WebhookMessageDelete deletes a webhook message.
// webhookID : The ID of a webhook
// token     : The auth token for the webhook
// messageID : The ID of a message to delete
func (s *Session) WebhookMessageDelete(webhookID, token, messageID string, options ...RequestOption) (err error) {
	return s.WebhookMessageDeleteWithOptions(webhookID, token, messageID, WebhookMessageOptions{}, options...)
}

// WebhookMessageDeleteWithOptions deletes a webhook message with query-string options.
func (s *Session) WebhookMessageDeleteWithOptions(webhookID, token, messageID string, messageOptions WebhookMessageOptions, options ...RequestOption) (err error) {
	uri := webhookMessageURI(webhookID, token, messageID, messageOptions.ThreadID, false)

	options = withoutAuthorizationOptions(options)
	_, err = s.requestWithBucketIDNoGlobal("DELETE", uri, nil, webhookMessageBucketID(webhookID), options...)
	return
}

func webhookMessageURI(webhookID, token, messageID, threadID string, withComponents bool) string {
	uri := EndpointWebhookMessage(webhookID, token, messageID)
	values := url.Values{}
	if threadID != "" {
		values.Set("thread_id", threadID)
	}
	if withComponents {
		values.Set("with_components", "true")
	}
	if query := values.Encode(); query != "" {
		uri += "?" + query
	}
	return uri
}

func (s *Session) interactionWebhookMessageDelete(appID, token, messageID string, options ...RequestOption) (err error) {
	uri := EndpointWebhookMessage(appID, token, messageID)

	options = withoutAuthorizationOptions(options)
	_, err = s.requestWithBucketIDNoGlobalTTL("DELETE", uri, nil, interactionWebhookMessageBucketID(appID, token), interactionWebhookBucketTTL, options...)
	return
}

func webhookTokenBucketID(webhookID string) string {
	return EndpointWebhookToken(webhookID, "")
}

func webhookMessageBucketID(webhookID string) string {
	return EndpointWebhookMessage(webhookID, "", "")
}

func interactionWebhookTokenBucketID(appID, token string) string {
	return EndpointWebhookToken(appID, interactionTokenBucketKey(token))
}

func interactionWebhookMessageBucketID(appID, token string) string {
	return EndpointWebhookMessage(appID, interactionTokenBucketKey(token), "")
}

func interactionTokenBucketKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256-" + hex.EncodeToString(sum[:])
}

func interactionResponseBucketID(interactionID string) string {
	return EndpointInteractionResponse(interactionID, "")
}

// MessageReactionAdd creates an emoji reaction to a message.
// channelID : The channel ID.
// messageID : The message ID.
// emojiID   : Either the unicode emoji for the reaction, or a guild emoji identifier in name:id format (e.g. "hello:1234567654321")
func (s *Session) MessageReactionAdd(channelID, messageID, emojiID string, options ...RequestOption) error {

	// emoji such as  #⃣ need to have # escaped
	emojiID = strings.Replace(emojiID, "#", "%23", -1)
	_, err := s.RequestWithBucketID("PUT", EndpointMessageReaction(channelID, messageID, emojiID, "@me"), nil, EndpointMessageReaction(channelID, "", "", ""), options...)

	return err
}

// MessageReactionRemove deletes an emoji reaction to a message.
// channelID : The channel ID.
// messageID : The message ID.
// emojiID   : Either the unicode emoji for the reaction, or a guild emoji identifier.
// userID	 : @me or ID of the user to delete the reaction for.
func (s *Session) MessageReactionRemove(channelID, messageID, emojiID, userID string, options ...RequestOption) error {

	// emoji such as  #⃣ need to have # escaped
	emojiID = strings.Replace(emojiID, "#", "%23", -1)
	_, err := s.RequestWithBucketID("DELETE", EndpointMessageReaction(channelID, messageID, emojiID, userID), nil, EndpointMessageReaction(channelID, "", "", ""), options...)

	return err
}

// MessageReactionsRemoveAll deletes all reactions from a message
// channelID : The channel ID
// messageID : The message ID.
func (s *Session) MessageReactionsRemoveAll(channelID, messageID string, options ...RequestOption) error {

	_, err := s.RequestWithBucketID("DELETE", EndpointMessageReactionsAll(channelID, messageID), nil, EndpointMessageReactionsAll(channelID, messageID), options...)

	return err
}

// MessageReactionsRemoveEmoji deletes all reactions of a certain emoji from a message
// channelID : The channel ID
// messageID : The message ID
// emojiID   : The emoji ID
func (s *Session) MessageReactionsRemoveEmoji(channelID, messageID, emojiID string, options ...RequestOption) error {

	// emoji such as  #⃣ need to have # escaped
	emojiID = strings.Replace(emojiID, "#", "%23", -1)
	_, err := s.RequestWithBucketID("DELETE", EndpointMessageReactions(channelID, messageID, emojiID), nil, EndpointMessageReactions(channelID, messageID, emojiID), options...)

	return err
}

// MessageReactions gets all the users reactions for a specific emoji.
// channelID : The channel ID.
// messageID : The message ID.
// emojiID   : Either the unicode emoji for the reaction, or a guild emoji identifier.
// limit    : max number of users to return (max 100)
// beforeID  : If provided all reactions returned will be before given ID.
// afterID   : If provided all reactions returned will be after given ID.
func (s *Session) MessageReactions(channelID, messageID, emojiID string, limit int, beforeID, afterID string, options ...RequestOption) (st []*User, err error) {
	return s.MessageReactionsByType(channelID, messageID, emojiID, ReactionTypeNormal, limit, beforeID, afterID, options...)
}

// MessageReactionsByType gets all the users who used a specific reaction type for an emoji.
func (s *Session) MessageReactionsByType(channelID, messageID, emojiID string, reactionType ReactionType, limit int, beforeID, afterID string, options ...RequestOption) (st []*User, err error) {
	// emoji such as  #⃣ need to have # escaped
	emojiID = strings.Replace(emojiID, "#", "%23", -1)
	uri := EndpointMessageReactions(channelID, messageID, emojiID)

	v := url.Values{}
	v.Set("type", strconv.Itoa(int(reactionType)))

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if afterID != "" {
		v.Set("after", afterID)
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointMessageReaction(channelID, "", "", ""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to threads
// ------------------------------------------------------------------------------------------------

// MessageThreadStartComplex creates a new thread from an existing message.
// channelID : Channel to create thread in
// messageID : Message to start thread from
// data : Parameters of the thread
func (s *Session) MessageThreadStartComplex(channelID, messageID string, data *ThreadStart, options ...RequestOption) (ch *Channel, err error) {
	endpoint := EndpointChannelMessageThread(channelID, messageID)
	var body []byte
	body, err = s.RequestWithBucketID("POST", endpoint, data, EndpointChannelMessageThread(channelID, ""), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &ch)
	return
}

// MessageThreadStart creates a new thread from an existing message.
// channelID       : Channel to create thread in
// messageID       : Message to start thread from
// name            : Name of the thread
// archiveDuration : Auto archive duration (in minutes)
func (s *Session) MessageThreadStart(channelID, messageID string, name string, archiveDuration int, options ...RequestOption) (ch *Channel, err error) {
	return s.MessageThreadStartComplex(channelID, messageID, &ThreadStart{
		Name:                name,
		AutoArchiveDuration: archiveDuration,
	}, options...)
}

// ThreadStartComplex creates a new thread.
// channelID : Channel to create thread in
// data : Parameters of the thread
func (s *Session) ThreadStartComplex(channelID string, data *ThreadStart, options ...RequestOption) (ch *Channel, err error) {
	endpoint := EndpointChannelThreads(channelID)
	var body []byte
	body, err = s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &ch)
	return
}

// ThreadStart creates a new thread.
// channelID       : Channel to create thread in
// name            : Name of the thread
// archiveDuration : Auto archive duration (in minutes)
func (s *Session) ThreadStart(channelID, name string, typ ChannelType, archiveDuration int, options ...RequestOption) (ch *Channel, err error) {
	return s.ThreadStartComplex(channelID, &ThreadStart{
		Name:                name,
		Type:                typ,
		AutoArchiveDuration: archiveDuration,
	}, options...)
}

// ForumThreadStartComplex starts a new thread (creates a post) in a forum channel.
// channelID   : Channel to create thread in.
// threadData  : Parameters of the thread.
// messageData : Parameters of the starting message.
func (s *Session) ForumThreadStartComplex(channelID string, threadData *ThreadStart, messageData *MessageSend, options ...RequestOption) (th *Channel, err error) {
	if threadData == nil {
		return nil, fmt.Errorf("thread data cannot be nil")
	}
	if messageData == nil {
		return nil, fmt.Errorf("message send data cannot be nil")
	}

	endpoint := EndpointChannelThreads(channelID)

	// TODO: Remove this when compatibility is not required.
	if messageData.Embed != nil {
		if messageData.Embeds == nil {
			messageData.Embeds = []*MessageEmbed{messageData.Embed}
		} else {
			err = fmt.Errorf("cannot specify both Embed and Embeds")
			return
		}
	}

	if err = setEmbedTypes(messageData.Embeds); err != nil {
		return
	}
	s.applyAllowedMentionsToMessage(messageData)

	// TODO: Remove this when compatibility is not required.
	files := messageData.Files
	if messageData.File != nil {
		if files == nil {
			files = []*File{messageData.File}
		} else {
			err = fmt.Errorf("cannot specify both File and Files")
			return
		}
	}

	data := struct {
		*ThreadStart
		Message *MessageSend `json:"message"`
	}{ThreadStart: threadData, Message: messageData}

	var response []byte
	if len(files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(data, files)
		if encodeErr != nil {
			return th, encodeErr
		}

		response, err = s.RequestRaw("POST", endpoint, contentType, body, endpoint, 0, options...)
	} else {
		response, err = s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	}
	if err != nil {
		return
	}

	err = unmarshal(response, &th)
	return
}

// ForumThreadStart starts a new thread (post) in a forum channel.
// channelID       : Channel to create thread in.
// name            : Name of the thread.
// archiveDuration : Auto archive duration.
// content         : Content of the starting message.
func (s *Session) ForumThreadStart(channelID, name string, archiveDuration int, content string, options ...RequestOption) (th *Channel, err error) {
	return s.ForumThreadStartComplex(channelID, &ThreadStart{
		Name:                name,
		AutoArchiveDuration: archiveDuration,
	}, &MessageSend{Content: content}, options...)
}

// ForumThreadStartEmbed starts a new thread (post) in a forum channel.
// channelID       : Channel to create thread in.
// name            : Name of the thread.
// archiveDuration : Auto archive duration.
// embed           : Embed data of the starting message.
func (s *Session) ForumThreadStartEmbed(channelID, name string, archiveDuration int, embed *MessageEmbed, options ...RequestOption) (th *Channel, err error) {
	return s.ForumThreadStartComplex(channelID, &ThreadStart{
		Name:                name,
		AutoArchiveDuration: archiveDuration,
	}, &MessageSend{Embeds: []*MessageEmbed{embed}}, options...)
}

// ForumThreadStartEmbeds starts a new thread (post) in a forum channel.
// channelID       : Channel to create thread in.
// name            : Name of the thread.
// archiveDuration : Auto archive duration.
// embeds          : Embeds data of the starting message.
func (s *Session) ForumThreadStartEmbeds(channelID, name string, archiveDuration int, embeds []*MessageEmbed, options ...RequestOption) (th *Channel, err error) {
	return s.ForumThreadStartComplex(channelID, &ThreadStart{
		Name:                name,
		AutoArchiveDuration: archiveDuration,
	}, &MessageSend{Embeds: embeds}, options...)
}

// ThreadJoin adds current user to a thread
func (s *Session) ThreadJoin(id string, options ...RequestOption) error {
	endpoint := EndpointThreadMember(id, "@me")
	_, err := s.RequestWithBucketID("PUT", endpoint, nil, EndpointThreadMember(id, ""), options...)
	return err
}

// ThreadLeave removes current user to a thread
func (s *Session) ThreadLeave(id string, options ...RequestOption) error {
	endpoint := EndpointThreadMember(id, "@me")
	_, err := s.RequestWithBucketID("DELETE", endpoint, nil, EndpointThreadMember(id, ""), options...)
	return err
}

// ThreadMemberAdd adds another member to a thread
func (s *Session) ThreadMemberAdd(threadID, memberID string, options ...RequestOption) error {
	endpoint := EndpointThreadMember(threadID, memberID)
	_, err := s.RequestWithBucketID("PUT", endpoint, nil, EndpointThreadMember(threadID, ""), options...)
	return err
}

// ThreadMemberRemove removes another member from a thread
func (s *Session) ThreadMemberRemove(threadID, memberID string, options ...RequestOption) error {
	endpoint := EndpointThreadMember(threadID, memberID)
	_, err := s.RequestWithBucketID("DELETE", endpoint, nil, EndpointThreadMember(threadID, ""), options...)
	return err
}

// ThreadMember returns thread member object for the specified member of a thread.
// withMember : Whether to include a guild member object.
func (s *Session) ThreadMember(threadID, memberID string, withMember bool, options ...RequestOption) (member *ThreadMember, err error) {
	uri := EndpointThreadMember(threadID, memberID)

	queryParams := url.Values{}
	if withMember {
		queryParams.Set("with_member", "true")
	}

	if len(queryParams) > 0 {
		uri += "?" + queryParams.Encode()
	}

	var body []byte
	body, err = s.RequestWithBucketID("GET", uri, nil, EndpointThreadMember(threadID, ""), options...)

	if err != nil {
		return
	}

	err = unmarshal(body, &member)
	return
}

// ThreadMembers returns all members of specified thread.
// limit      : Max number of thread members to return (1-100). Defaults to 100.
// afterID    : Get thread members after this user ID.
// withMember : Whether to include a guild member object for each thread member.
func (s *Session) ThreadMembers(threadID string, limit int, withMember bool, afterID string, options ...RequestOption) (members []*ThreadMember, err error) {
	uri := EndpointThreadMembers(threadID)

	queryParams := url.Values{}
	if withMember {
		queryParams.Set("with_member", "true")
	}
	if limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}
	if afterID != "" {
		queryParams.Set("after", afterID)
	}

	if len(queryParams) > 0 {
		uri += "?" + queryParams.Encode()
	}

	var body []byte
	body, err = s.RequestWithBucketID("GET", uri, nil, EndpointThreadMembers(threadID), options...)

	if err != nil {
		return
	}

	err = unmarshal(body, &members)
	return
}

// ThreadsActive returns all active threads for specified channel.
func (s *Session) ThreadsActive(channelID string, options ...RequestOption) (threads *ThreadsList, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointChannelActiveThreads(channelID), nil, EndpointChannelActiveThreads(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// GuildThreadsActive returns all active threads for specified guild.
func (s *Session) GuildThreadsActive(guildID string, options ...RequestOption) (threads *ThreadsList, err error) {
	var body []byte
	body, err = s.RequestWithBucketID("GET", EndpointGuildActiveThreads(guildID), nil, EndpointGuildActiveThreads(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// ThreadsSearch returns threads matching the search options in a channel.
func (s *Session) ThreadsSearch(channelID string, opts *ThreadSearchOptions, options ...RequestOption) (result *ThreadSearchResult, err error) {
	endpoint := EndpointChannelThreadsSearch(channelID)
	uri := endpoint

	if opts != nil {
		queryParams := url.Values{}
		if opts.Name != "" {
			queryParams.Set("name", opts.Name)
		}
		if opts.Slop != nil {
			queryParams.Set("slop", strconv.Itoa(*opts.Slop))
		}
		if opts.MinID != "" {
			queryParams.Set("min_id", opts.MinID)
		}
		if opts.MaxID != "" {
			queryParams.Set("max_id", opts.MaxID)
		}
		for _, tag := range opts.Tags {
			queryParams.Add("tag", tag)
		}
		if opts.TagSetting != "" {
			queryParams.Set("tag_setting", string(opts.TagSetting))
		}
		if opts.Archived != nil {
			queryParams.Set("archived", strconv.FormatBool(*opts.Archived))
		}
		if opts.SortBy != "" {
			queryParams.Set("sort_by", string(opts.SortBy))
		}
		if opts.SortOrder != "" {
			queryParams.Set("sort_order", string(opts.SortOrder))
		}
		if opts.Limit > 0 {
			queryParams.Set("limit", strconv.Itoa(opts.Limit))
		}
		if opts.Offset > 0 {
			queryParams.Set("offset", strconv.Itoa(opts.Offset))
		}
		if len(queryParams) > 0 {
			uri += "?" + queryParams.Encode()
		}
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		var restErr *RESTError
		if errors.As(err, &restErr) && restErr.Response != nil && restErr.Response.StatusCode == http.StatusAccepted {
			var indexErr *ThreadSearchIndexNotReadyError
			if decodeErr := unmarshal(restErr.ResponseBody, &indexErr); decodeErr == nil && indexErr != nil {
				indexErr.RESTError = restErr
				return nil, indexErr
			}
		}
		return nil, err
	}

	if err = unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("%w: thread search response is null", ErrJSONUnmarshal)
	}
	return result, nil
}

// ThreadsArchived returns archived threads for specified channel.
// before : If specified returns only threads before the timestamp
// limit  : Optional maximum amount of threads to return.
func (s *Session) ThreadsArchived(channelID string, before *time.Time, limit int, options ...RequestOption) (threads *ThreadsList, err error) {
	endpoint := EndpointChannelPublicArchivedThreads(channelID)
	uri := endpoint
	v := url.Values{}
	if before != nil {
		v.Set("before", before.Format(time.RFC3339))
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}

	var body []byte
	body, err = s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// ThreadsPrivateArchived returns archived private threads for specified channel.
// before : If specified returns only threads before the timestamp
// limit  : Optional maximum amount of threads to return.
func (s *Session) ThreadsPrivateArchived(channelID string, before *time.Time, limit int, options ...RequestOption) (threads *ThreadsList, err error) {
	endpoint := EndpointChannelPrivateArchivedThreads(channelID)
	uri := endpoint
	v := url.Values{}
	if before != nil {
		v.Set("before", before.Format(time.RFC3339))
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}
	var body []byte
	body, err = s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// ThreadsPrivateJoinedArchived returns archived joined private threads for specified channel.
// before : If specified returns only threads before the timestamp
// limit  : Optional maximum amount of threads to return.
func (s *Session) ThreadsPrivateJoinedArchived(channelID string, before *time.Time, limit int, options ...RequestOption) (threads *ThreadsList, err error) {
	endpoint := EndpointChannelJoinedPrivateArchivedThreads(channelID)
	uri := endpoint
	v := url.Values{}
	if before != nil {
		v.Set("before", before.Format(time.RFC3339))
	}

	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}

	if len(v) > 0 {
		uri += "?" + v.Encode()
	}
	var body []byte
	body, err = s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &threads)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to application (slash) commands
// ------------------------------------------------------------------------------------------------

// ApplicationCommandCreate creates a global application command and returns it.
// appID       : The application ID.
// guildID     : Guild ID to create guild-specific application command. If empty - creates global application command.
// cmd         : New application command data.
func (s *Session) ApplicationCommandCreate(appID string, guildID string, cmd *ApplicationCommand, options ...RequestOption) (ccmd *ApplicationCommand, err error) {
	if cmd == nil {
		return nil, fmt.Errorf("application command data cannot be nil")
	}

	endpoint := EndpointApplicationGlobalCommands(appID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommands(appID, guildID)
	}

	body, err := s.RequestWithBucketID("POST", endpoint, *cmd, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &ccmd)

	return
}

// ApplicationCommandEdit edits application command and returns new command data.
// appID       : The application ID.
// cmdID       : Application command ID to edit.
// guildID     : Guild ID to edit guild-specific application command. If empty - edits global application command.
// cmd         : Updated application command data.
func (s *Session) ApplicationCommandEdit(appID, guildID, cmdID string, cmd *ApplicationCommand, options ...RequestOption) (updated *ApplicationCommand, err error) {
	if cmd == nil {
		return nil, fmt.Errorf("application command data cannot be nil")
	}

	endpoint := EndpointApplicationGlobalCommand(appID, cmdID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommand(appID, guildID, cmdID)
	}

	body, err := s.RequestWithBucketID("PATCH", endpoint, *cmd, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &updated)

	return
}

// ApplicationCommandBulkOverwrite Creates commands overwriting existing commands. Returns a list of commands.
// appID    : The application ID.
// commands : The commands to create.
func (s *Session) ApplicationCommandBulkOverwrite(appID string, guildID string, commands []*ApplicationCommand, options ...RequestOption) (createdCommands []*ApplicationCommand, err error) {
	endpoint := EndpointApplicationGlobalCommands(appID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommands(appID, guildID)
	}

	body, err := s.RequestWithBucketID("PUT", endpoint, commands, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &createdCommands)

	return
}

// ApplicationCommandDelete deletes application command by ID.
// appID       : The application ID.
// cmdID       : Application command ID to delete.
// guildID     : Guild ID to delete guild-specific application command. If empty - deletes global application command.
func (s *Session) ApplicationCommandDelete(appID, guildID, cmdID string, options ...RequestOption) error {
	endpoint := EndpointApplicationGlobalCommand(appID, cmdID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommand(appID, guildID, cmdID)
	}

	_, err := s.RequestWithBucketID("DELETE", endpoint, nil, endpoint, options...)

	return err
}

// ApplicationCommand retrieves an application command by given ID.
// appID       : The application ID.
// cmdID       : Application command ID.
// guildID     : Guild ID to retrieve guild-specific application command. If empty - retrieves global application command.
func (s *Session) ApplicationCommand(appID, guildID, cmdID string, options ...RequestOption) (cmd *ApplicationCommand, err error) {
	endpoint := EndpointApplicationGlobalCommand(appID, cmdID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommand(appID, guildID, cmdID)
	}

	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &cmd)

	return
}

// ApplicationCommands retrieves all commands in application.
// appID       : The application ID.
// guildID     : Guild ID to retrieve all guild-specific application commands. If empty - retrieves global application commands.
func (s *Session) ApplicationCommands(appID, guildID string, options ...RequestOption) (cmd []*ApplicationCommand, err error) {
	endpoint := EndpointApplicationGlobalCommands(appID)
	if guildID != "" {
		endpoint = EndpointApplicationGuildCommands(appID, guildID)
	}

	body, err := s.RequestWithBucketID("GET", endpoint+"?with_localizations=true", nil, "GET "+endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &cmd)

	return
}

// GuildApplicationCommandsPermissions returns permissions for application commands in a guild.
// appID       : The application ID
// guildID     : Guild ID to retrieve application commands permissions for.
func (s *Session) GuildApplicationCommandsPermissions(appID, guildID string, options ...RequestOption) (permissions []*GuildApplicationCommandPermissions, err error) {
	endpoint := EndpointApplicationCommandsGuildPermissions(appID, guildID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &permissions)
	return
}

// ApplicationCommandPermissions returns all permissions of an application command
// appID       : The Application ID
// guildID     : The guild ID containing the application command
// cmdID       : The command ID to retrieve the permissions of
func (s *Session) ApplicationCommandPermissions(appID, guildID, cmdID string, options ...RequestOption) (permissions *GuildApplicationCommandPermissions, err error) {
	endpoint := EndpointApplicationCommandPermissions(appID, guildID, cmdID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &permissions)
	return
}

// ApplicationCommandPermissionsEdit edits the permissions of an application command
// appID       : The Application ID
// guildID     : The guild ID containing the application command
// cmdID       : The command ID to edit the permissions of
// permissions : An object containing a list of permissions for the application command
//
// NOTE: Requires OAuth2 token with applications.commands.permissions.update scope
func (s *Session) ApplicationCommandPermissionsEdit(appID, guildID, cmdID string, permissions *ApplicationCommandPermissionsList, options ...RequestOption) (err error) {
	endpoint := EndpointApplicationCommandPermissions(appID, guildID, cmdID)

	_, err = s.RequestWithBucketID("PUT", endpoint, permissions, endpoint, options...)
	return
}

// ApplicationCommandPermissionsBatchEdit edits the permissions of a batch of commands
// appID       : The Application ID
// guildID     : The guild ID to batch edit commands of
// permissions : A list of permissions paired with a command ID, guild ID, and application ID per application command
//
// NOTE: This endpoint has been disabled with updates to command permissions (Permissions v2). Please use ApplicationCommandPermissionsEdit instead.
func (s *Session) ApplicationCommandPermissionsBatchEdit(appID, guildID string, permissions []*GuildApplicationCommandPermissions, options ...RequestOption) (err error) {
	endpoint := EndpointApplicationCommandsGuildPermissions(appID, guildID)

	_, err = s.RequestWithBucketID("PUT", endpoint, permissions, endpoint, options...)
	return
}

// InteractionRespond creates the response to an interaction.
// interaction : Interaction instance.
// resp        : Response message data.
func (s *Session) InteractionRespond(interaction *Interaction, resp *InteractionResponse, options ...RequestOption) error {
	_, err := s.interactionRespond(interaction, resp, false, options...)
	return err
}

// InteractionRespondWithResponse creates a response to an interaction and returns its callback data.
func (s *Session) InteractionRespondWithResponse(interaction *Interaction, resp *InteractionResponse, options ...RequestOption) (*InteractionCallbackResponse, error) {
	return s.interactionRespond(interaction, resp, true, options...)
}

func (s *Session) interactionRespond(interaction *Interaction, resp *InteractionResponse, withResponse bool, options ...RequestOption) (*InteractionCallbackResponse, error) {
	if interaction == nil {
		return nil, fmt.Errorf("interaction cannot be nil")
	}
	if resp == nil {
		return nil, fmt.Errorf("interaction response cannot be nil")
	}

	endpoint := EndpointInteractionResponse(interaction.ID, interaction.Token)
	if withResponse {
		endpoint += "?with_response=true"
	}
	bucketID := interactionResponseBucketID(interaction.ID)
	options = withoutAuthorizationOptions(options)

	if resp.Data != nil {
		s.applyAllowedMentionsToInteractionResponse(resp.Data)
	}

	var response []byte
	var err error
	if resp.Data != nil && len(resp.Data.Files) > 0 {
		contentType, body, encodeErr := MultipartBodyWithJSON(resp, resp.Data.Files)
		if encodeErr != nil {
			return nil, encodeErr
		}

		response, err = s.requestRawWithEphemeralBucket("POST", endpoint, contentType, body, bucketID, 0, false, options...)
	} else {
		response, err = s.requestWithEphemeralBucketID("POST", endpoint, *resp, bucketID, false, options...)
	}
	if err != nil || !withResponse {
		return nil, err
	}

	var callback *InteractionCallbackResponse
	if err := unmarshal(response, &callback); err != nil {
		return nil, err
	}
	return callback, nil
}

// InteractionResponse gets the response to an interaction.
// interaction : Interaction instance.
func (s *Session) InteractionResponse(interaction *Interaction, options ...RequestOption) (*Message, error) {
	if interaction == nil {
		return nil, fmt.Errorf("interaction cannot be nil")
	}

	return s.interactionWebhookMessage(interaction.AppID, interaction.Token, "@original", options...)
}

// InteractionResponseEdit edits the response to an interaction.
// interaction : Interaction instance.
// newresp     : Updated response message data.
func (s *Session) InteractionResponseEdit(interaction *Interaction, newresp *WebhookEdit, options ...RequestOption) (*Message, error) {
	if interaction == nil {
		return nil, fmt.Errorf("interaction cannot be nil")
	}

	return s.interactionWebhookMessageEdit(interaction.AppID, interaction.Token, "@original", newresp, options...)
}

// InteractionResponseDelete deletes the response to an interaction.
// interaction : Interaction instance.
func (s *Session) InteractionResponseDelete(interaction *Interaction, options ...RequestOption) error {
	if interaction == nil {
		return fmt.Errorf("interaction cannot be nil")
	}

	return s.interactionWebhookMessageDelete(interaction.AppID, interaction.Token, "@original", options...)
}

// FollowupMessageCreate creates the followup message for an interaction.
// interaction : Interaction instance.
// wait        : Waits for server confirmation of message send and ensures that the return struct is populated (it is nil otherwise)
// data        : Data of the message to send.
func (s *Session) FollowupMessageCreate(interaction *Interaction, wait bool, data *WebhookParams, options ...RequestOption) (*Message, error) {
	if interaction == nil {
		return nil, fmt.Errorf("interaction cannot be nil")
	}

	return s.interactionWebhookExecute(interaction.AppID, interaction.Token, wait, data, options...)
}

// FollowupMessageEdit edits a followup message of an interaction.
// interaction : Interaction instance.
// messageID   : The followup message ID.
// data        : Data to update the message
func (s *Session) FollowupMessageEdit(interaction *Interaction, messageID string, data *WebhookEdit, options ...RequestOption) (*Message, error) {
	if interaction == nil {
		return nil, fmt.Errorf("interaction cannot be nil")
	}

	return s.interactionWebhookMessageEdit(interaction.AppID, interaction.Token, messageID, data, options...)
}

// FollowupMessageDelete deletes a followup message of an interaction.
// interaction : Interaction instance.
// messageID   : The followup message ID.
func (s *Session) FollowupMessageDelete(interaction *Interaction, messageID string, options ...RequestOption) error {
	if interaction == nil {
		return fmt.Errorf("interaction cannot be nil")
	}

	return s.interactionWebhookMessageDelete(interaction.AppID, interaction.Token, messageID, options...)
}

// ------------------------------------------------------------------------------------------------
// Functions specific to stage instances
// ------------------------------------------------------------------------------------------------

// StageInstanceCreate creates and returns a new Stage instance associated to a Stage channel.
// data : Parameters needed to create a stage instance.
// data : The data of the Stage instance to create
func (s *Session) StageInstanceCreate(data *StageInstanceParams, options ...RequestOption) (si *StageInstance, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointStageInstances, data, EndpointStageInstances, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &si)
	return
}

// StageInstance will retrieve a Stage instance by ID of the Stage channel.
// channelID : The ID of the Stage channel
func (s *Session) StageInstance(channelID string, options ...RequestOption) (si *StageInstance, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointStageInstance(channelID), nil, EndpointStageInstance(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &si)
	return
}

// StageInstanceEdit will edit a Stage instance by ID of the Stage channel.
// channelID : The ID of the Stage channel
// data : The data to edit the Stage instance
func (s *Session) StageInstanceEdit(channelID string, data *StageInstanceParams, options ...RequestOption) (si *StageInstance, err error) {

	body, err := s.RequestWithBucketID("PATCH", EndpointStageInstance(channelID), data, EndpointStageInstance(channelID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &si)
	return
}

// StageInstanceDelete will delete a Stage instance by ID of the Stage channel.
// channelID : The ID of the Stage channel
func (s *Session) StageInstanceDelete(channelID string, options ...RequestOption) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointStageInstance(channelID), nil, EndpointStageInstance(channelID), options...)
	return
}

// ------------------------------------------------------------------------------------------------
// Functions specific to guilds scheduled events
// ------------------------------------------------------------------------------------------------

// GuildScheduledEvents returns an array of GuildScheduledEvent for a guild
// guildID        : The ID of a Guild
// userCount      : Whether to include the user count in the response
func (s *Session) GuildScheduledEvents(guildID string, userCount bool, options ...RequestOption) (st []*GuildScheduledEvent, err error) {
	uri := EndpointGuildScheduledEvents(guildID)
	if userCount {
		uri += "?with_user_count=true"
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildScheduledEvents(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEvent returns a specific GuildScheduledEvent in a guild
// guildID        : The ID of a Guild
// eventID        : The ID of the event
// userCount      : Whether to include the user count in the response
func (s *Session) GuildScheduledEvent(guildID, eventID string, userCount bool, options ...RequestOption) (st *GuildScheduledEvent, err error) {
	uri := EndpointGuildScheduledEvent(guildID, eventID)
	if userCount {
		uri += "?with_user_count=true"
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildScheduledEvent(guildID, eventID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEventCreate creates a GuildScheduledEvent for a guild and returns it
// guildID   : The ID of a Guild
// eventID   : The ID of the event
func (s *Session) GuildScheduledEventCreate(guildID string, event *GuildScheduledEventParams, options ...RequestOption) (st *GuildScheduledEvent, err error) {
	body, err := s.RequestWithBucketID("POST", EndpointGuildScheduledEvents(guildID), event, EndpointGuildScheduledEvents(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEventEdit updates a specific event for a guild and returns it.
// guildID   : The ID of a Guild
// eventID   : The ID of the event
func (s *Session) GuildScheduledEventEdit(guildID, eventID string, event *GuildScheduledEventParams, options ...RequestOption) (st *GuildScheduledEvent, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointGuildScheduledEvent(guildID, eventID), event, EndpointGuildScheduledEvent(guildID, eventID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEventDelete deletes a specific GuildScheduledEvent in a guild
// guildID   : The ID of a Guild
// eventID   : The ID of the event
func (s *Session) GuildScheduledEventDelete(guildID, eventID string, options ...RequestOption) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildScheduledEvent(guildID, eventID), nil, EndpointGuildScheduledEvent(guildID, eventID), options...)
	return
}

// GuildScheduledEventExceptionCreate creates an exception to a recurring event.
func (s *Session) GuildScheduledEventExceptionCreate(guildID, eventID string, params *GuildScheduledEventExceptionCreateParams, options ...RequestOption) (exception *GuildScheduledEventException, err error) {
	if params == nil {
		return nil, fmt.Errorf("guild scheduled event exception data cannot be nil")
	}

	endpoint := EndpointGuildScheduledEventExceptions(guildID, eventID)
	body, err := s.RequestWithBucketID("POST", endpoint, params, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &exception); err != nil {
		return nil, err
	}
	if exception == nil {
		return nil, fmt.Errorf("%w: guild scheduled event exception response is null", ErrJSONUnmarshal)
	}
	return exception, nil
}

// GuildScheduledEventExceptionEdit edits an exception to a recurring event.
func (s *Session) GuildScheduledEventExceptionEdit(guildID, eventID, exceptionID string, params *GuildScheduledEventExceptionEditParams, options ...RequestOption) (exception *GuildScheduledEventException, err error) {
	if params == nil {
		return nil, fmt.Errorf("guild scheduled event exception data cannot be nil")
	}

	endpoint := EndpointGuildScheduledEventException(guildID, eventID, exceptionID)
	body, err := s.RequestWithBucketID("PATCH", endpoint, params, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &exception); err != nil {
		return nil, err
	}
	if exception == nil {
		return nil, fmt.Errorf("%w: guild scheduled event exception response is null", ErrJSONUnmarshal)
	}
	return exception, nil
}

// GuildScheduledEventExceptionDelete deletes an exception to a recurring event.
func (s *Session) GuildScheduledEventExceptionDelete(guildID, eventID, exceptionID string, options ...RequestOption) (err error) {
	endpoint := EndpointGuildScheduledEventException(guildID, eventID, exceptionID)
	_, err = s.RequestWithBucketID("DELETE", endpoint, nil, endpoint, options...)
	return
}

// GuildScheduledEventUsers returns an array of GuildScheduledEventUser for a particular event in a guild
// guildID    : The ID of a Guild
// eventID    : The ID of the event
// limit      : The maximum number of users to return (Max 100)
// withMember : Whether to include the member object in the response
// beforeID   : If is not empty all returned users entries will be before the given ID
// afterID    : If is not empty all returned users entries will be after the given ID
func (s *Session) GuildScheduledEventUsers(guildID, eventID string, limit int, withMember bool, beforeID, afterID string, options ...RequestOption) (st []*GuildScheduledEventUser, err error) {
	uri := EndpointGuildScheduledEventUsers(guildID, eventID)

	queryParams := url.Values{}
	if withMember {
		queryParams.Set("with_member", "true")
	}
	if limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}
	if beforeID != "" {
		queryParams.Set("before", beforeID)
	}
	if afterID != "" {
		queryParams.Set("after", afterID)
	}

	if len(queryParams) > 0 {
		uri += "?" + queryParams.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointGuildScheduledEventUsers(guildID, eventID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildScheduledEventExceptionUsers returns users subscribed to an event exception.
func (s *Session) GuildScheduledEventExceptionUsers(guildID, eventID, exceptionID string, limit int, withMember bool, beforeID, afterID string, options ...RequestOption) (users []*GuildScheduledEventUser, err error) {
	endpoint := EndpointGuildScheduledEventExceptionUsers(guildID, eventID, exceptionID)
	queryParams := url.Values{}
	if withMember {
		queryParams.Set("with_member", "true")
	}
	if limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}
	if beforeID != "" {
		queryParams.Set("before", beforeID)
	}
	if afterID != "" {
		queryParams.Set("after", afterID)
	}

	uri := endpoint
	if len(queryParams) > 0 {
		uri += "?" + queryParams.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &users)
	return
}

// GuildScheduledEventUserCounts returns subscriber counts for an event and up to 10 exceptions.
func (s *Session) GuildScheduledEventUserCounts(guildID, eventID string, exceptionIDs []string, options ...RequestOption) (counts *GuildScheduledEventUserCounts, err error) {
	if len(exceptionIDs) > 10 {
		return nil, fmt.Errorf("guild scheduled event user counts accepts at most 10 exception ids")
	}

	endpoint := EndpointGuildScheduledEventUserCounts(guildID, eventID)
	queryParams := url.Values{}
	for _, exceptionID := range exceptionIDs {
		queryParams.Add("guild_scheduled_event_exception_ids", exceptionID)
	}

	uri := endpoint
	if len(queryParams) > 0 {
		uri += "?" + queryParams.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &counts); err != nil {
		return nil, err
	}
	if counts == nil {
		return nil, fmt.Errorf("%w: guild scheduled event user counts response is null", ErrJSONUnmarshal)
	}
	if counts.GuildScheduledEventExceptionCounts == nil {
		return nil, fmt.Errorf("%w: guild scheduled event user counts response is missing exception counts", ErrJSONUnmarshal)
	}
	return counts, nil
}

// GuildWelcomeScreen returns the welcome screen of a guild.
// guildID : The ID of the guild
func (s *Session) GuildWelcomeScreen(guildID string, options ...RequestOption) (welcomeScreen *GuildWelcomeScreen, err error) {
	endpoint := EndpointGuildWelcomeScreen(guildID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &welcomeScreen); err != nil {
		return nil, err
	}
	if welcomeScreen == nil {
		return nil, fmt.Errorf("%w: guild welcome screen response is null", ErrJSONUnmarshal)
	}
	return welcomeScreen, nil
}

// GuildWelcomeScreenEdit modifies the welcome screen of a guild.
// guildID : The ID of the guild
// data    : The welcome screen fields to modify
func (s *Session) GuildWelcomeScreenEdit(guildID string, data *GuildWelcomeScreenParams, options ...RequestOption) (welcomeScreen *GuildWelcomeScreen, err error) {
	if data == nil {
		return nil, fmt.Errorf("guild welcome screen data cannot be nil")
	}

	endpoint := EndpointGuildWelcomeScreen(guildID)
	body, err := s.RequestWithBucketID("PATCH", endpoint, data, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &welcomeScreen); err != nil {
		return nil, err
	}
	if welcomeScreen == nil {
		return nil, fmt.Errorf("%w: guild welcome screen response is null", ErrJSONUnmarshal)
	}
	return welcomeScreen, nil
}

// GuildHomeSettings returns the home settings shown to new guild members.
// It returns nil when the guild has no home settings.
// guildID : The ID of the guild
func (s *Session) GuildHomeSettings(guildID string, options ...RequestOption) (settings *GuildHomeSettings, err error) {
	endpoint := EndpointGuildNewMemberWelcome(guildID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, nil
	}

	if err = unmarshal(body, &settings); err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, fmt.Errorf("%w: guild home settings response is null", ErrJSONUnmarshal)
	}
	return settings, nil
}

// GuildOnboarding returns onboarding configuration of a guild.
// guildID   : The ID of the guild
func (s *Session) GuildOnboarding(guildID string, options ...RequestOption) (onboarding *GuildOnboarding, err error) {
	endpoint := EndpointGuildOnboarding(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &onboarding)
	return
}

// GuildOnboardingEdit edits onboarding configuration of a guild.
// guildID   : The ID of the guild
// o         : New GuildOnboarding data
func (s *Session) GuildOnboardingEdit(guildID string, o *GuildOnboarding, options ...RequestOption) (onboarding *GuildOnboarding, err error) {
	endpoint := EndpointGuildOnboarding(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("PUT", endpoint, o, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &onboarding)
	return
}

// GuildIncidentActionsEdit modifies the incident actions of a guild.
// guildID : The ID of the guild
// data    : The incident actions to modify
func (s *Session) GuildIncidentActionsEdit(guildID string, data *GuildIncidentActionsParams, options ...RequestOption) (incidents *GuildIncidentsData, err error) {
	if data == nil {
		return nil, fmt.Errorf("guild incident actions data cannot be nil")
	}

	endpoint := EndpointGuildIncidentActions(guildID)
	body, err := s.RequestWithBucketID("PUT", endpoint, data, endpoint, options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &incidents); err != nil {
		return nil, err
	}
	if incidents == nil {
		return nil, fmt.Errorf("%w: guild incident actions response is null", ErrJSONUnmarshal)
	}
	return incidents, nil
}

// ----------------------------------------------------------------------
// Functions specific to auto moderation
// ----------------------------------------------------------------------

// AutoModerationRules returns a list of auto moderation rules.
// guildID : ID of the guild
func (s *Session) AutoModerationRules(guildID string, options ...RequestOption) (st []*AutoModerationRule, err error) {
	endpoint := EndpointGuildAutoModerationRules(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// AutoModerationRule returns an auto moderation rule.
// guildID : ID of the guild
// ruleID  : ID of the auto moderation rule
func (s *Session) AutoModerationRule(guildID, ruleID string, options ...RequestOption) (st *AutoModerationRule, err error) {
	endpoint := EndpointGuildAutoModerationRule(guildID, ruleID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// AutoModerationRuleCreate creates an auto moderation rule with the given data and returns it.
// guildID : ID of the guild
// rule    : Rule data
func (s *Session) AutoModerationRuleCreate(guildID string, rule *AutoModerationRule, options ...RequestOption) (st *AutoModerationRule, err error) {
	endpoint := EndpointGuildAutoModerationRules(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("POST", endpoint, rule, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// AutoModerationRuleEdit edits and returns the updated auto moderation rule.
// guildID : ID of the guild
// ruleID  : ID of the auto moderation rule
// rule    : New rule data
func (s *Session) AutoModerationRuleEdit(guildID, ruleID string, rule *AutoModerationRule, options ...RequestOption) (st *AutoModerationRule, err error) {
	endpoint := EndpointGuildAutoModerationRule(guildID, ruleID)

	var body []byte
	body, err = s.RequestWithBucketID("PATCH", endpoint, rule, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// AutoModerationRuleDelete deletes an auto moderation rule.
// guildID : ID of the guild
// ruleID  : ID of the auto moderation rule
func (s *Session) AutoModerationRuleDelete(guildID, ruleID string, options ...RequestOption) (err error) {
	endpoint := EndpointGuildAutoModerationRule(guildID, ruleID)
	_, err = s.RequestWithBucketID("DELETE", endpoint, nil, endpoint, options...)
	return
}

// ApplicationRoleConnectionMetadata returns application role connection metadata.
// appID : ID of the application
func (s *Session) ApplicationRoleConnectionMetadata(appID string) (st []*ApplicationRoleConnectionMetadata, err error) {
	endpoint := EndpointApplicationRoleConnectionMetadata(appID)
	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ApplicationRoleConnectionMetadataUpdate updates and returns application role connection metadata.
// appID    : ID of the application
// metadata : New metadata
func (s *Session) ApplicationRoleConnectionMetadataUpdate(appID string, metadata []*ApplicationRoleConnectionMetadata) (st []*ApplicationRoleConnectionMetadata, err error) {
	endpoint := EndpointApplicationRoleConnectionMetadata(appID)
	var body []byte
	body, err = s.RequestWithBucketID("PUT", endpoint, metadata, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserApplicationRoleConnection returns user role connection to the specified application.
// appID : ID of the application
func (s *Session) UserApplicationRoleConnection(appID string) (st *ApplicationRoleConnection, err error) {
	endpoint := EndpointUserApplicationRoleConnection(appID)
	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return

}

// UserApplicationRoleConnectionUpdate updates and returns user role connection to the specified application.
// appID      : ID of the application
// connection : New ApplicationRoleConnection data
func (s *Session) UserApplicationRoleConnectionUpdate(appID string, rconn *ApplicationRoleConnection) (st *ApplicationRoleConnection, err error) {
	endpoint := EndpointUserApplicationRoleConnection(appID)
	var body []byte
	body, err = s.RequestWithBucketID("PUT", endpoint, rconn, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// UserApplicationRoleConnectionDelete deletes the current user's role connection to the specified application.
// appID : ID of the application
func (s *Session) UserApplicationRoleConnectionDelete(appID string, options ...RequestOption) (err error) {
	endpoint := EndpointUserApplicationRoleConnection(appID)
	_, err = s.RequestWithBucketID("DELETE", endpoint, nil, endpoint, options...)
	return
}

// UserApplicationEntitlements returns the current user's entitlements for the specified application.
// appID         : ID of the application.
// filterOptions : Optional filter options; otherwise set it to nil.
func (s *Session) UserApplicationEntitlements(appID string, filterOptions *UserApplicationEntitlementFilterOptions, options ...RequestOption) (entitlements []*Entitlement, err error) {
	endpoint := EndpointUserApplicationEntitlements(appID)
	uri := endpoint

	if filterOptions != nil {
		queryParams := url.Values{}
		for _, skuID := range filterOptions.SKUIDs {
			queryParams.Add("sku_ids", skuID)
		}
		if filterOptions.ExcludeConsumed != nil {
			queryParams.Set("exclude_consumed", strconv.FormatBool(*filterOptions.ExcludeConsumed))
		}
		if len(queryParams) > 0 {
			uri += "?" + queryParams.Encode()
		}
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}
	if err = unmarshal(body, &entitlements); err != nil {
		return nil, err
	}
	if entitlements == nil {
		return nil, fmt.Errorf("%w: current user application entitlements response is null", ErrJSONUnmarshal)
	}
	return entitlements, nil
}

// ----------------------------------------------------------------------
// Functions specific to polls
// ----------------------------------------------------------------------

// PollAnswerVoters returns users who voted for a particular answer in a poll on the specified message.
// channelID : ID of the channel.
// messageID : ID of the message.
// answerID  : ID of the answer.
func (s *Session) PollAnswerVoters(channelID, messageID string, answerID int, options ...RequestOption) (voters []*User, err error) {
	return s.PollAnswerVotersWithOptions(channelID, messageID, answerID, nil, options...)
}

// PollAnswerVotersWithOptions returns a page of users who voted for a particular poll answer.
func (s *Session) PollAnswerVotersWithOptions(channelID, messageID string, answerID int, query *PollAnswerVotersOptions, options ...RequestOption) (voters []*User, err error) {
	endpoint := EndpointPollAnswerVoters(channelID, messageID, answerID)
	uri := endpoint
	if query != nil {
		values := url.Values{}
		if query.After != "" {
			values.Set("after", query.After)
		}
		if query.Limit > 0 {
			values.Set("limit", strconv.Itoa(query.Limit))
		}
		if encoded := values.Encode(); encoded != "" {
			uri += "?" + encoded
		}
	}

	var body []byte
	body, err = s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return
	}

	var r struct {
		Users []*User `json:"users"`
	}

	err = unmarshal(body, &r)
	if err != nil {
		return
	}

	voters = r.Users
	return
}

// PollExpire expires poll on the specified message.
// channelID : ID of the channel.
// messageID : ID of the message.
func (s *Session) PollExpire(channelID, messageID string, options ...RequestOption) (msg *Message, err error) {
	endpoint := EndpointPollExpire(channelID, messageID)

	var body []byte
	body, err = s.RequestWithBucketID("POST", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &msg)
	return
}

// ----------------------------------------------------------------------
// Functions specific to monetization
// ----------------------------------------------------------------------

// SKUs returns all SKUs for a given application.
// appID : The ID of the application.
func (s *Session) SKUs(appID string) (skus []*SKU, err error) {
	endpoint := EndpointApplicationSKUs(appID)

	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint)
	if err != nil {
		return
	}

	err = unmarshal(body, &skus)
	return
}

// Entitlements returns all Entitlements for a given app, active and expired.
// appID			: The ID of the application.
// filterOptions	: Optional filter options; otherwise set it to nil.
func (s *Session) Entitlements(appID string, filterOptions *EntitlementFilterOptions, options ...RequestOption) (entitlements []*Entitlement, err error) {
	endpoint := EndpointEntitlements(appID)

	queryParams := url.Values{}
	if filterOptions != nil {
		if filterOptions.UserID != "" {
			queryParams.Set("user_id", filterOptions.UserID)
		}
		if filterOptions.SkuIDs != nil && len(filterOptions.SkuIDs) > 0 {
			queryParams.Set("sku_ids", strings.Join(filterOptions.SkuIDs, ","))
		}
		if filterOptions.Before != nil {
			queryParams.Set("before", filterOptions.Before.Format(time.RFC3339))
		}
		if filterOptions.After != nil {
			queryParams.Set("after", filterOptions.After.Format(time.RFC3339))
		}
		if filterOptions.Limit > 0 {
			queryParams.Set("limit", strconv.Itoa(filterOptions.Limit))
		}
		if filterOptions.GuildID != "" {
			queryParams.Set("guild_id", filterOptions.GuildID)
		}
		if filterOptions.ExcludeEnded {
			queryParams.Set("exclude_ended", "true")
		}
	}

	body, err := s.RequestWithBucketID("GET", endpoint+"?"+queryParams.Encode(), nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &entitlements)
	return
}

// Entitlement returns an entitlement for a given application.
// appID         : The ID of the application.
// entitlementID : The ID of the entitlement.
func (s *Session) Entitlement(appID, entitlementID string, options ...RequestOption) (entitlement *Entitlement, err error) {
	endpoint := EndpointEntitlement(appID, entitlementID)

	body, err := s.RequestWithBucketID("GET", endpoint, nil, EndpointEntitlement(appID, ""), options...)
	if err != nil {
		return nil, err
	}

	if err = unmarshal(body, &entitlement); err != nil {
		return nil, err
	}
	if entitlement == nil {
		return nil, fmt.Errorf("%w: entitlement response is null", ErrJSONUnmarshal)
	}
	return entitlement, nil
}

// EntitlementConsume marks a given One-Time Purchase for the user as consumed.
func (s *Session) EntitlementConsume(appID, entitlementID string, options ...RequestOption) (err error) {
	_, err = s.RequestWithBucketID("POST", EndpointEntitlementConsume(appID, entitlementID), nil, EndpointEntitlementConsume(appID, ""), options...)
	return
}

// EntitlementTestCreate creates a test entitlement to a given SKU for a given guild or user.
// Discord will act as though that user or guild has entitlement to your premium offering.
func (s *Session) EntitlementTestCreate(appID string, data *EntitlementTest, options ...RequestOption) (err error) {
	endpoint := EndpointEntitlements(appID)

	_, err = s.RequestWithBucketID("POST", endpoint, data, endpoint, options...)
	return
}

// EntitlementTestDelete deletes a currently-active test entitlement. Discord will act as though
// that user or guild no longer has entitlement to your premium offering.
func (s *Session) EntitlementTestDelete(appID, entitlementID string, options ...RequestOption) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointEntitlement(appID, entitlementID), nil, EndpointEntitlement(appID, ""), options...)
	return
}

// Subscriptions returns all subscriptions containing the SKU.
// skuID : The ID of the SKU.
// userID : User ID for which to return subscriptions. Required except for OAuth queries.
// before : Optional timestamp to retrieve subscriptions before this time.
// after : Optional timestamp to retrieve subscriptions after this time.
// limit : Optional maximum number of subscriptions to return (1-100, default 50).
func (s *Session) Subscriptions(skuID string, userID string, before, after *time.Time, limit int, options ...RequestOption) (subscriptions []*Subscription, err error) {
	endpoint := EndpointSubscriptions(skuID)

	queryParams := url.Values{}
	if before != nil {
		queryParams.Set("before", before.Format(time.RFC3339))
	}
	if after != nil {
		queryParams.Set("after", after.Format(time.RFC3339))
	}
	if userID != "" {
		queryParams.Set("user_id", userID)
	}
	if limit > 0 {
		queryParams.Set("limit", strconv.Itoa(limit))
	}

	body, err := s.RequestWithBucketID("GET", endpoint+"?"+queryParams.Encode(), nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &subscriptions)
	return
}

// Subscription returns a subscription by its SKU and subscription ID.
// skuID : The ID of the SKU.
// subscriptionID : The ID of the subscription.
// userID : User ID for which to return the subscription. Required except for OAuth queries.
func (s *Session) Subscription(skuID, subscriptionID, userID string, options ...RequestOption) (subscription *Subscription, err error) {
	endpoint := EndpointSubscription(skuID, subscriptionID)

	queryParams := url.Values{}
	if userID != "" {
		// Unlike stated in the documentation, the user_id parameter is required here.
		queryParams.Set("user_id", userID)
	}

	body, err := s.RequestWithBucketID("GET", endpoint+"?"+queryParams.Encode(), nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &subscription)
	return
}

// UserVoiceState returns a user's voice state in a guild.
// guildID : The ID of the guild.
// userID  : The ID of the user.
// Note: Using @me will return the bot's voice state for the given guild.
func (s *Session) UserVoiceState(guildID string, userID string, options ...RequestOption) (state *VoiceState, err error) {
	endpoint := EndpointGuildMemberVoiceState(guildID, userID)

	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &state)
	return
}

// CurrentUserVoiceStateEdit updates the current user's voice state in a guild.
// guildID : The ID of the guild.
// data    : Updated current user voice state data.
func (s *Session) CurrentUserVoiceStateEdit(guildID string, data *CurrentUserVoiceStateEditParams, options ...RequestOption) (err error) {
	endpoint := EndpointGuildMemberVoiceState(guildID, "@me")
	_, err = s.RequestWithBucketID("PATCH", endpoint, data, endpoint, options...)
	return
}

// UserVoiceStateEdit updates another user's voice state in a guild.
// guildID : The ID of the guild.
// userID  : The ID of the user.
// data    : Updated user voice state data.
func (s *Session) UserVoiceStateEdit(guildID, userID string, data *UserVoiceStateEditParams, options ...RequestOption) (err error) {
	endpoint := EndpointGuildMemberVoiceState(guildID, userID)
	_, err = s.RequestWithBucketID("PATCH", endpoint, data, endpoint, options...)
	return
}

// ----------------------------------------------------------------------
// Functions specific to lobbies
// ----------------------------------------------------------------------

// LobbyCreate creates a lobby owned by the current application using a Bot token.
func (s *Session) LobbyCreate(params *LobbyParams, options ...RequestOption) (lobby *Lobby, err error) {
	if params == nil {
		params = &LobbyParams{}
	}

	body, err := s.RequestWithBucketID("POST", EndpointLobbies, params, EndpointLobbies, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &lobby)
	return
}

// LobbyCreateOrJoin creates or joins a lobby using a Bearer token with the sdk.social_layer scope.
func (s *Session) LobbyCreateOrJoin(params *LobbyCreateOrJoinParams, options ...RequestOption) (lobby *Lobby, err error) {
	body, err := s.RequestWithBucketID("PUT", EndpointLobbies, params, EndpointLobbies, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &lobby)
	return
}

// Lobby returns the lobby with the given ID.
func (s *Session) Lobby(lobbyID string, options ...RequestOption) (lobby *Lobby, err error) {
	endpoint := EndpointLobby(lobbyID)
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &lobby)
	return
}

// LobbyEdit modifies a lobby using a Bot token.
func (s *Session) LobbyEdit(lobbyID string, params *LobbyParams, options ...RequestOption) (lobby *Lobby, err error) {
	if params == nil {
		params = &LobbyParams{}
	}

	endpoint := EndpointLobby(lobbyID)
	body, err := s.RequestWithBucketID("PATCH", endpoint, params, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &lobby)
	return
}

// LobbyDelete deletes a lobby using a Bot token.
func (s *Session) LobbyDelete(lobbyID string, options ...RequestOption) (err error) {
	endpoint := EndpointLobby(lobbyID)
	_, err = s.RequestWithBucketID("DELETE", endpoint, nil, endpoint, options...)
	return
}

// LobbyMemberAdd adds or updates a lobby member using a Bot token.
func (s *Session) LobbyMemberAdd(lobbyID, userID string, params *LobbyMemberParams, options ...RequestOption) (member *LobbyMember, err error) {
	data := LobbyMemberParams{}
	if params != nil {
		data = *params
	}
	data.ID = ""

	endpoint := EndpointLobbyMember(lobbyID, userID)
	body, err := s.RequestWithBucketID("PUT", endpoint, &data, EndpointLobbyMember(lobbyID, ""), options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &member)
	return
}

// LobbyMembersBulkUpdate adds, updates, or removes up to 25 lobby members using a Bot token.
func (s *Session) LobbyMembersBulkUpdate(lobbyID string, members []LobbyMemberUpdateParams, options ...RequestOption) (updated []*LobbyMember, err error) {
	endpoint := EndpointLobbyMembersBulk(lobbyID)
	body, err := s.RequestWithBucketID("POST", endpoint, members, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &updated)
	return
}

// LobbyMemberRemove removes a lobby member using a Bot token.
func (s *Session) LobbyMemberRemove(lobbyID, userID string, options ...RequestOption) (err error) {
	endpoint := EndpointLobbyMember(lobbyID, userID)
	_, err = s.RequestWithBucketID("DELETE", endpoint, nil, EndpointLobbyMember(lobbyID, ""), options...)
	return
}

// LobbyLeave removes the current user using a Bearer token.
func (s *Session) LobbyLeave(lobbyID string, options ...RequestOption) (err error) {
	endpoint := EndpointLobbyMember(lobbyID, "@me")
	_, err = s.RequestWithBucketID("DELETE", endpoint, nil, EndpointLobbyMember(lobbyID, ""), options...)
	return
}

// LobbyChannelLink links a text channel using a Bearer token. The current user
// must have LobbyMemberFlagCanLinkLobby.
func (s *Session) LobbyChannelLink(lobbyID, channelID string, options ...RequestOption) (lobby *Lobby, err error) {
	endpoint := EndpointLobbyChannelLinking(lobbyID)
	params := struct {
		ChannelID string `json:"channel_id"`
	}{
		ChannelID: channelID,
	}

	body, err := s.RequestWithBucketID("PATCH", endpoint, params, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &lobby)
	return
}

// LobbyChannelUnlink unlinks the current text channel using a Bearer token.
// The current user must have LobbyMemberFlagCanLinkLobby.
func (s *Session) LobbyChannelUnlink(lobbyID string, options ...RequestOption) (lobby *Lobby, err error) {
	endpoint := EndpointLobbyChannelLinking(lobbyID)
	body, err := s.RequestWithBucketID("PATCH", endpoint, struct{}{}, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &lobby)
	return
}

// LobbyMessageSend sends a lobby message using a Bearer token with the sdk.social_layer scope.
func (s *Session) LobbyMessageSend(lobbyID string, params *LobbyMessageSendParams, options ...RequestOption) (message *LobbyMessage, err error) {
	endpoint := EndpointLobbyMessages(lobbyID)
	body, err := s.RequestWithBucketID("POST", endpoint, params, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &message)
	return
}

// LobbyMessages returns the most recent lobby messages using a Bearer token
// with the sdk.social_layer scope. Limit can be 1 to 200; zero uses Discord's default.
func (s *Session) LobbyMessages(lobbyID string, limit int, options ...RequestOption) (messages []*LobbyMessage, err error) {
	endpoint := EndpointLobbyMessages(lobbyID)
	uri := endpoint
	if limit > 0 {
		query := url.Values{}
		query.Set("limit", strconv.Itoa(limit))
		uri += "?" + query.Encode()
	}

	body, err := s.RequestWithBucketID("GET", uri, nil, endpoint, options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &messages)
	return
}

// LobbyMessageModerationMetadataUpdate sets app-scoped moderation metadata
// for a lobby message using a Bot token.
func (s *Session) LobbyMessageModerationMetadataUpdate(lobbyID, messageID string, metadata map[string]string, options ...RequestOption) (err error) {
	endpoint := EndpointLobbyMessageModerationMetadata(lobbyID, messageID)
	bucketID := EndpointLobbyMessageModerationMetadata(lobbyID, "")
	_, err = s.RequestWithBucketID("PUT", endpoint, metadata, bucketID, options...)
	return
}

// LobbyChannelInviteCreate creates a linked-channel invite for the current user
// using a Bearer token with the sdk.social_layer scope.
func (s *Session) LobbyChannelInviteCreate(lobbyID string, options ...RequestOption) (invite *LobbyInvite, err error) {
	return s.lobbyChannelInviteCreate(lobbyID, "@me", options...)
}

// LobbyChannelInviteCreateForUser creates a linked-channel invite for a user using a Bot token.
func (s *Session) LobbyChannelInviteCreateForUser(lobbyID, userID string, options ...RequestOption) (invite *LobbyInvite, err error) {
	return s.lobbyChannelInviteCreate(lobbyID, userID, options...)
}

func (s *Session) lobbyChannelInviteCreate(lobbyID, userID string, options ...RequestOption) (invite *LobbyInvite, err error) {
	endpoint := EndpointLobbyMemberInvites(lobbyID, userID)
	body, err := s.RequestWithBucketID("POST", endpoint, nil, EndpointLobbyMemberInvites(lobbyID, ""), options...)
	if err != nil {
		return nil, err
	}

	err = unmarshal(body, &invite)
	return
}
