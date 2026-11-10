package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var enc = base64.RawURLEncoding

// fakeIdP is a provider with an RSA and an EC key; token hands out what the
// test put in next.
type fakeIdP struct {
	*httptest.Server
	rsaKey   *rsa.PrivateKey
	ecKey    *ecdsa.PrivateKey
	mu       sync.Mutex
	next     string
	form     url.Values
	jwksHits atomic.Int32
}

func newIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{}
	f.rsaKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	f.ecKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"issuer": f.URL, "authorization_endpoint": f.URL + "/auth",
				"token_endpoint": f.URL + "/token", "jwks_uri": f.URL + "/jwks"})
		case "/jwks":
			f.jwksHits.Add(1)
			pad := func(b []byte) string { return enc.EncodeToString(append(make([]byte, 32-len(b)), b...)) }
			json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
				{"kty": "RSA", "kid": "r1", "alg": "RS256", "use": "sig", "n": enc.EncodeToString(f.rsaKey.N.Bytes()), "e": enc.EncodeToString(big.NewInt(int64(f.rsaKey.E)).Bytes())},
				{"kty": "EC", "kid": "e1", "crv": "P-256", "x": pad(f.ecKey.X.Bytes()), "y": pad(f.ecKey.Y.Bytes())},
			}})
		case "/token":
			r.ParseForm()
			f.mu.Lock()
			f.form = r.PostForm
			tok := f.next
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"id_token": tok, "token_type": "Bearer"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// sign makes a token: alg RS256, ES256, HS256 or none, with this kid.
func (f *fakeIdP) sign(alg, kid string, claims map[string]any) string {
	head, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signed := enc.EncodeToString(head) + "." + enc.EncodeToString(body)
	sum := sha256.Sum256([]byte(signed))
	var sig []byte
	switch alg {
	case "RS256":
		sig, _ = rsa.SignPKCS1v15(rand.Reader, f.rsaKey, crypto.SHA256, sum[:])
	case "ES256":
		r, s, _ := ecdsa.Sign(rand.Reader, f.ecKey, sum[:])
		sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	case "HS256":
		// Signed with the public RSA modulus as the "secret": the classic key-confusion attack.
		m := hmac.New(sha256.New, []byte(enc.EncodeToString(f.rsaKey.N.Bytes())))
		m.Write([]byte(signed))
		sig = m.Sum(nil)
	}
	return signed + "." + enc.EncodeToString(sig)
}

func (f *fakeIdP) claims(nonce string) map[string]any {
	return map[string]any{"iss": f.URL, "aud": "heimdeck", "sub": "u-1", "preferred_username": "luis",
		"nonce": nonce, "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(), "groups": []string{"admins"}}
}

func TestLogin(t *testing.T) {
	f := newIdP(t)
	ctx := context.Background()
	p, err := Discover(ctx, http.DefaultClient, f.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	c := Config{Issuer: f.URL, ClientID: "heimdeck", ClientSecret: "s3cret", Scopes: []string{"openid", "profile"}, RedirectURL: "https://heim.example/api/oidc/callback"}
	req := NewRequest()

	// PKCE: the browser gets the challenge, the token request the verifier.
	u, _ := url.Parse(p.AuthURL(c, req))
	sum := sha256.Sum256([]byte(req.Verifier))
	if q := u.Query(); q.Get("code_challenge") != enc.EncodeToString(sum[:]) || q.Get("code_challenge_method") != "S256" || q.Get("state") != req.State || q.Get("nonce") != req.Nonce {
		t.Fatalf("auth URL: %s", u)
	}
	if strings.Contains(u.String(), req.Verifier) || strings.Contains(u.String(), "s3cret") {
		t.Fatal("the verifier or the secret is in the browser's address")
	}

	login := func(token string) (Claims, error) {
		f.mu.Lock()
		f.next = token
		f.mu.Unlock()
		return p.Exchange(ctx, c, req, "code-1")
	}
	for _, alg := range []string{"RS256", "ES256"} {
		kid := map[string]string{"RS256": "r1", "ES256": "e1"}[alg]
		got, err := login(f.sign(alg, kid, f.claims(req.Nonce)))
		if err != nil || got.Subject != "u-1" || got.PreferredUsername != "luis" || len(got.Groups) != 1 || got.Groups[0] != "admins" {
			t.Fatalf("%s: %+v %v", alg, got, err)
		}
	}
	f.mu.Lock()
	verifier := f.form.Get("code_verifier")
	f.mu.Unlock()
	if verifier != req.Verifier {
		t.Fatalf("token request without the verifier: %q", verifier)
	}

	bad := func(what string, mutate func(m map[string]any)) {
		t.Helper()
		m := f.claims(req.Nonce)
		mutate(m)
		if _, err := login(f.sign("RS256", "r1", m)); err == nil {
			t.Fatalf("%s was accepted", what)
		}
	}
	bad("another issuer", func(m map[string]any) { m["iss"] = "https://evil.example" })
	bad("another audience", func(m map[string]any) { m["aud"] = "other-app" })
	bad("several audiences, issued to another", func(m map[string]any) { m["aud"] = []string{"heimdeck", "other"}; m["azp"] = "other" })
	bad("an expired token", func(m map[string]any) { m["exp"] = time.Now().Add(-10 * time.Minute).Unix() })
	bad("a token without expiry", func(m map[string]any) { delete(m, "exp") })
	bad("a token from the future", func(m map[string]any) { m["iat"] = time.Now().Add(time.Hour).Unix() })
	bad("another login's nonce", func(m map[string]any) { m["nonce"] = "replayed" })
	bad("a token without subject", func(m map[string]any) { delete(m, "sub") })

	// Forged signatures: none, HMAC with the public key, a changed payload, a stranger's key.
	if _, err := login(f.sign("none", "r1", f.claims(req.Nonce))); err == nil {
		t.Fatal("alg none was accepted")
	}
	if _, err := login(f.sign("HS256", "r1", f.claims(req.Nonce))); err == nil {
		t.Fatal("HS256 with the public key was accepted")
	}
	good := strings.Split(f.sign("RS256", "r1", f.claims(req.Nonce)), ".")
	m := f.claims(req.Nonce)
	m["sub"] = "admin"
	body, _ := json.Marshal(m)
	if _, err := login(good[0] + "." + enc.EncodeToString(body) + "." + good[2]); err == nil {
		t.Fatal("a changed payload kept its signature")
	}
	stranger := newIdP(t)
	if _, err := login(stranger.sign("RS256", "r1", f.claims(req.Nonce))); err == nil {
		t.Fatal("a token signed by another key was accepted")
	}
	if _, err := login(f.sign("ES256", "r1", f.claims(req.Nonce))); err == nil {
		t.Fatal("an EC signature checked against the RSA key was accepted")
	}

	// An unknown key ID loads the keys again – but not for every forged token.
	before := f.jwksHits.Load()
	for i := 0; i < 5; i++ {
		login(f.sign("RS256", fmt.Sprint("unknown-", i), f.claims(req.Nonce)))
	}
	if hits := f.jwksHits.Load() - before; hits > 1 {
		t.Fatalf("the keys were loaded %d times for forged key IDs", hits)
	}
}

// Discovery checks that the provider is the issuer it was asked for.
func TestDiscoverIssuer(t *testing.T) {
	f := newIdP(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": f.URL, "authorization_endpoint": "x", "token_endpoint": "x", "jwks_uri": "x"})
	}))
	defer other.Close()
	if _, err := Discover(context.Background(), http.DefaultClient, other.URL); err == nil {
		t.Fatal("a provider naming another issuer was accepted")
	}
	short, _ := rsa.GenerateKey(rand.Reader, 1024)
	k := jwk{Kty: "RSA", N: enc.EncodeToString(short.N.Bytes()), E: enc.EncodeToString(big.NewInt(int64(short.E)).Bytes())}
	sum := sha256.Sum256([]byte("a.b"))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, short, crypto.SHA256, sum[:])
	if checkSignature("RS256", k, []byte("a.b"), sig) == nil {
		t.Fatal("a 1024-bit RSA key was accepted")
	}
}
