// Package oidc is the client side of OpenID Connect – the login with an
// identity provider such as Authentik, Authelia, Keycloak, Pocket ID or
// Zitadel. It covers what Heimdeck needs: discovery, the authorization code
// flow with PKCE, and checking the ID token (signature from the provider's
// keys, issuer, audience, expiry, nonce).
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Provider is the discovery document of an issuer, plus its keys.
type Provider struct {
	Issuer        string `json:"issuer"`
	AuthEndpoint  string `json:"authorization_endpoint"`
	TokenEndpoint string `json:"token_endpoint"`
	UserInfo      string `json:"userinfo_endpoint"`
	JWKSURI       string `json:"jwks_uri"`
	EndSession    string `json:"end_session_endpoint"`

	client  *http.Client
	mu      sync.Mutex
	keys    map[string]jwk
	keysAt  time.Time
	fetched time.Time
}

// Config is what the admin enters for the login.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       []string
	RedirectURL  string
}

// Claims are the facts about the person that Heimdeck uses.
type Claims struct {
	Subject           string   `json:"sub"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Email             string   `json:"email"`
	Groups            []string `json:"-"`
	Nonce             string   `json:"nonce"`
}

var cache sync.Map // issuer -> *Provider

// Discover loads the provider's configuration (cached for an hour).
func Discover(ctx context.Context, client *http.Client, issuer string) (*Provider, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if v, ok := cache.Load(issuer); ok && time.Since(v.(*Provider).fetched) < time.Hour {
		return v.(*Provider), nil
	}
	p := &Provider{client: client}
	if err := getJSON(ctx, client, issuer+"/.well-known/openid-configuration", "", p); err != nil {
		return nil, fmt.Errorf("Anbieter nicht gefunden: %w", err)
	}
	if strings.TrimRight(p.Issuer, "/") != issuer {
		return nil, fmt.Errorf("der Anbieter meldet sich als %q statt %q – stimmt die Issuer-Adresse?", p.Issuer, issuer)
	}
	if p.AuthEndpoint == "" || p.TokenEndpoint == "" || p.JWKSURI == "" {
		return nil, errors.New("die Anbieter-Konfiguration ist unvollständig")
	}
	p.fetched = time.Now()
	cache.Store(issuer, p)
	return p, nil
}

// Request is one login in progress: what goes to the provider and what has
// to come back.
type Request struct {
	State, Nonce, Verifier string
}

func random() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewRequest prepares a login.
func NewRequest() Request { return Request{State: random(), Nonce: random(), Verifier: random()} }

// AuthURL is where the browser is sent to log in.
func (p *Provider) AuthURL(c Config, r Request) string {
	sum := sha256.Sum256([]byte(r.Verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.ClientID},
		"redirect_uri":          {c.RedirectURL},
		"scope":                 {strings.Join(c.Scopes, " ")},
		"state":                 {r.State},
		"nonce":                 {r.Nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(p.AuthEndpoint, "?") {
		sep = "&"
	}
	return p.AuthEndpoint + sep + q.Encode()
}

// Exchange trades the code from the callback for the person's verified claims.
func (p *Provider) Exchange(ctx context.Context, c Config, r Request, code string) (Claims, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.RedirectURL},
		"code_verifier": {r.Verifier},
		"client_id":     {c.ClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Claims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(url.QueryEscape(c.ClientID), url.QueryEscape(c.ClientSecret))
	resp, err := p.client.Do(req)
	if err != nil {
		return Claims{}, err
	}
	defer resp.Body.Close()
	var tok struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok)
	if resp.StatusCode != 200 || tok.IDToken == "" {
		msg := tok.Error
		if tok.Description != "" {
			msg += ": " + tok.Description
		}
		if msg == "" {
			msg = resp.Status
		}
		return Claims{}, fmt.Errorf("Anmeldung vom Anbieter abgelehnt (%s) – stimmen Client-ID und Client-Secret?", msg)
	}
	raw, err := p.verify(ctx, tok.IDToken, c.ClientID)
	if err != nil {
		return Claims{}, err
	}
	var claims Claims
	json.Unmarshal(raw, &claims)
	if claims.Nonce != r.Nonce {
		return Claims{}, errors.New("Anmeldung ungültig (nonce)")
	}
	if claims.Subject == "" {
		return Claims{}, errors.New("der Anbieter nennt keine Benutzer-ID (sub)")
	}
	claims.Groups = groups(raw)
	// Some providers (e.g. Authelia) put names and groups only into userinfo.
	if p.UserInfo != "" && tok.AccessToken != "" && (claims.Groups == nil || claims.PreferredUsername == "" || claims.Email == "") {
		var info json.RawMessage
		if getJSON(ctx, p.client, p.UserInfo, tok.AccessToken, &info) == nil {
			var u Claims
			json.Unmarshal(info, &u)
			if u.Subject == claims.Subject {
				if claims.PreferredUsername == "" {
					claims.PreferredUsername = u.PreferredUsername
				}
				if claims.Email == "" {
					claims.Email = u.Email
				}
				if claims.Name == "" {
					claims.Name = u.Name
				}
				if claims.Groups == nil {
					claims.Groups = groups(info)
				}
			}
		}
	}
	return claims, nil
}

// groups reads the group claim; providers send names as strings, some as
// objects or a single string.
func groups(raw []byte) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m["groups"] == nil {
		return nil
	}
	var list []string
	if json.Unmarshal(m["groups"], &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(m["groups"], &one) == nil {
		return []string{one}
	}
	var objs []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(m["groups"], &objs) == nil {
		for _, o := range objs {
			list = append(list, o.Name)
		}
	}
	return list
}

func getJSON(ctx context.Context, client *http.Client, u, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s antwortet mit %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
