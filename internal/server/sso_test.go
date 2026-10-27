package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIdP is a minimal OpenID Connect provider: it signs ID tokens for
// whoever the test says is logging in.
type fakeIdP struct {
	*httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	codes  map[string]map[string]any // code -> claims
	user   map[string]any            // the person logging in next
	badSig bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeIdP{key: key, codes: map[string]map[string]any{}}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize",
			"token_endpoint": f.URL + "/token", "jwks_uri": f.URL + "/jwks", "userinfo_endpoint": f.URL + "/userinfo"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	})
	// The "login page": remembers who logs in and sends the browser back with a code.
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		claims := map[string]any{"iss": f.URL, "aud": q.Get("client_id"), "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": q.Get("nonce")}
		f.mu.Lock()
		for k, v := range f.user {
			claims[k] = v
		}
		code := "code-" + q.Get("state")[:8]
		f.codes[code] = claims
		f.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if id, secret, _ := r.BasicAuth(); id != "heimdeck" || secret != "s3cret" || r.Form.Get("code_verifier") == "" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		}
		f.mu.Lock()
		claims := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		if claims == nil {
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": f.sign(claims), "access_token": "at"})
	})
	return f
}

func (f *fakeIdP) sign(claims map[string]any) string {
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signed := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signed))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if f.badSig {
		sig[0] ^= 0xff
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// ssoLogin walks a browser through the login and returns where it ended up.
func ssoLogin(t *testing.T, tc *testClient, path string) string {
	t.Helper()
	tc.c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Path == "/" { // back on Heimdeck's page: stop here
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, err := tc.c.Get(tc.base + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Header.Get("Location")
}

func TestSSOLogin(t *testing.T) {
	idp := newFakeIdP(t)
	st, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	cfg := map[string]any{"enabled": true, "issuer": idp.URL, "clientId": "heimdeck", "clientSecret": "s3cret", "label": "Authentik",
		"autoCreate": true, "allowedGroup": "heimdeck", "adminGroup": "heimdeck-admins", "passwordOff": true}
	if code, out := luis.call("PUT", "/api/sso", cfg); code != 200 {
		t.Fatalf("save sso: %d %v", code, out)
	}
	_, got := luis.call("GET", "/api/sso", nil)
	if got["secretSet"] != true || strings.Contains(fmt2(got), "s3cret") {
		t.Fatalf("secret leaked or missing: %v", got)
	}

	// A new person in the allowed group gets an account on the first login.
	idp.user = map[string]any{"sub": "u-anna", "preferred_username": "anna", "groups": []string{"heimdeck"}}
	anna := newTestClient(t, base)
	if to := ssoLogin(t, anna, "/api/oidc/login"); to != "/" {
		t.Fatalf("login ended at %q", to)
	}
	if _, state := anna.call("GET", "/api/state", nil); state["username"] != "anna" || state["role"] != "user" {
		t.Fatalf("not logged in as anna: %v", state)
	}

	// Someone outside the group is turned away.
	idp.user = map[string]any{"sub": "u-eve", "preferred_username": "eve", "groups": []string{"other"}}
	if to := ssoLogin(t, newTestClient(t, base), "/api/oidc/login"); to != "/?sso_error=group" {
		t.Fatalf("eve: %q", to)
	}
	// The name of an existing account is never taken over.
	idp.user = map[string]any{"sub": "u-fake", "preferred_username": "luis", "groups": []string{"heimdeck", "heimdeck-admins"}}
	fake := newTestClient(t, base)
	ssoLogin(t, fake, "/api/oidc/login")
	if _, state := fake.call("GET", "/api/state", nil); state["username"] != "luis-2" || state["role"] != "admin" {
		t.Fatalf("took over luis? %v", state)
	}

	// Password login is off – except for the main admin.
	if code, _ := newTestClient(t, base).call("POST", "/api/login", map[string]string{"username": "luis", "password": "admin-pw-123"}); code != 200 {
		t.Fatalf("main admin locked out: %d", code)
	}
	luis.call("POST", "/api/users", map[string]string{"username": "ben", "password": "ben-pw-1234"})
	ben := newTestClient(t, base)
	if code, _ := ben.call("POST", "/api/login", map[string]string{"username": "ben", "password": "ben-pw-1234"}); code != 403 {
		t.Fatalf("password login still works for ben: %d", code)
	}

	// The main admin links their own account, then logs in through the provider.
	idp.user = map[string]any{"sub": "u-luis", "preferred_username": "luis.blum", "groups": []string{"heimdeck"}}
	if to := ssoLogin(t, luis, "/api/oidc/login?link=1"); to != "/?sso=linked" {
		t.Fatalf("link: %q", to)
	}
	users, _ := st.Users()
	if !users[0].SSO {
		t.Fatal("not linked")
	}
	again := newTestClient(t, base)
	ssoLogin(t, again, "/api/oidc/login")
	if _, state := again.call("GET", "/api/state", nil); state["username"] != "luis" || state["role"] != "admin" {
		t.Fatalf("main admin through SSO: %v", state)
	}

	// A token with a broken signature is refused.
	idp.badSig = true
	idp.user = map[string]any{"sub": "u-anna", "groups": []string{"heimdeck"}}
	if to := ssoLogin(t, newTestClient(t, base), "/api/oidc/login"); to != "/?sso_error=provider" {
		t.Fatalf("bad signature: %q", to)
	}
	idp.badSig = false

	// The callback only works in the browser that started the login.
	mallory := newTestClient(t, base)
	mallory.c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if strings.HasPrefix(req.URL.String(), base+"/api/oidc/callback") {
			return http.ErrUseLastResponse
		}
		return nil
	}
	resp, _ := mallory.c.Get(base + "/api/oidc/login")
	resp.Body.Close()
	victim := newTestClient(t, base)
	victim.c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ = victim.c.Get(resp.Header.Get("Location"))
	resp.Body.Close()
	if resp.Header.Get("Location") != "/?sso_error=state" {
		t.Fatalf("login slipped into another browser: %q", resp.Header.Get("Location"))
	}
}

func fmt2(v any) string { b, _ := json.Marshal(v); return string(b) }
