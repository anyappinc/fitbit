package fitbit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var (
	// CSRFStateLength represents the length of `state` generating on authorization process.
	CSRFStateLength uint64 = 128

	// CodeVerifierLength represents the length of `code_verifier` generating on authorization process.
	CodeVerifierLength uint64 = 128
)

// Token represents the OAuth 2.0 Token.
type Token struct {
	AccessToken  string
	TokenType    string
	RefreshToken string
	Expiry       time.Time
}

func (t *Token) asOAuth2Token() *oauth2.Token {
	if t == nil {
		return nil
	}
	return &oauth2.Token{
		AccessToken:  t.AccessToken,
		TokenType:    t.TokenType,
		RefreshToken: t.RefreshToken,
		Expiry:       t.Expiry,
	}
}

type tokenRefresher struct {
	ctx       context.Context
	client    *Client
	lastToken *Token
}

// Token implements the the oauth2.TokenSource interface.
func (tkr *tokenRefresher) Token() (*oauth2.Token, error) {
	token, err := retrieveToken(
		tkr.ctx,
		tkr.client.oauth2Config.ClientID,
		tkr.client.oauth2Config.ClientSecret,
		tkr.client.oauth2Config.Endpoint.TokenURL,
		url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {tkr.lastToken.RefreshToken},
		},
		tkr.client.applicationType,
	)
	if err != nil {
		return nil, err
	}
	if tkr.client.updateTokenFunc != nil {
		if err := tkr.client.updateTokenFunc(tkr.lastToken, token); err != nil {
			return nil, err
		}
	}
	tkr.lastToken = token
	return token.asOAuth2Token(), err
}

type (
	// LinkResponse represents a response of the link request.
	LinkResponse struct {
		UserID string
		Scope  *Scope
		Token  *Token
	}
)

// AuthCodeURL returns an url to link with user's Fitbit account.
// Ref: https://dev.fitbit.com/build/reference/web-api/developer-guide/authorization/
// Ref: https://dev.fitbit.com/build/reference/web-api/authorization/authorize/
func (c *Client) AuthCodeURL(redirectURI string) (*url.URL, string, string) {
	state := string(randomBytes(CSRFStateLength))
	codeVerifier := randomBytes(CodeVerifierLength)
	hashedCodeVerifier := sha256.Sum256(codeVerifier)
	codeChallenge := base64.RawURLEncoding.EncodeToString(hashedCodeVerifier[:])
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", CodeChallengeMethod),
		oauth2.SetAuthURLParam("redirect_uri", redirectURI),
	}
	if c.debugMode {
		opts = append(opts, oauth2.ApprovalForce)
	}
	urlString := c.oauth2Config.AuthCodeURL(state, opts...)
	authCodeURL, _ := url.Parse(urlString) // error should never happen
	return authCodeURL, state, string(codeVerifier)
}

// Link obtains data for the user to interact with Fitbit APIs.
// Ref: https://dev.fitbit.com/build/reference/web-api/authorization/oauth2-token/
func (c *Client) Link(ctx context.Context, code, codeVerifier, reqURIString string) (*LinkResponse, error) {
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
		oauth2.SetAuthURLParam("redirect_uri", reqURIString),
	}
	if c.applicationType == ServerApplication {
		// `client_id` parameter seems unnecessary, but add this just to make sure
		// since this is noted "required" in the official document
		opts = append(opts, oauth2.SetAuthURLParam("client_id", c.oauth2Config.ClientID))
	}
	token, err := c.oauth2Config.Exchange(ctx, code, opts...)
	if err != nil {
		if rErr := (*oauth2.RetrieveError)(nil); errors.As(err, &rErr) {
			if e := parseError(rErr.Response, rErr.Body); e != nil {
				return nil, fmt.Errorf("fitbit(oauth2): cannot fetch token: %w", e)
			}
		}
		return nil, fmt.Errorf("fitbit(oauth2): cannot fetch token: %w", err)
	}
	return &LinkResponse{
		UserID: token.Extra("user_id").(string),
		Scope:  newScope(strings.Split(token.Extra("scope").(string), " ")),
		Token: &Token{
			AccessToken:  token.AccessToken,
			TokenType:    token.TokenType,
			RefreshToken: token.RefreshToken,
			Expiry:       token.Expiry,
		},
	}, nil
}
