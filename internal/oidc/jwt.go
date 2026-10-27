package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"slices"
	"strings"
	"time"
)

// jwk is one public key of the provider (RFC 7517).
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// keySet returns the provider's keys, loading them again when they are an
// hour old or a token names a key that is not known yet (key rotation).
func (p *Provider) keySet(ctx context.Context, kid string) (map[string]jwk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys != nil && time.Since(p.keysAt) < time.Hour {
		if _, ok := p.keys[kid]; ok || kid == "" {
			return p.keys, nil
		}
		if time.Since(p.keysAt) < 30*time.Second {
			return p.keys, nil // do not ask again for every forged token
		}
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := getJSON(ctx, p.client, p.JWKSURI, "", &set); err != nil {
		return nil, fmt.Errorf("Schlüssel des Anbieters nicht ladbar: %w", err)
	}
	p.keys = map[string]jwk{}
	for _, k := range set.Keys {
		if k.Use == "" || k.Use == "sig" {
			p.keys[k.Kid] = k
		}
	}
	p.keysAt = time.Now()
	return p.keys, nil
}

func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// verify checks the signature and the standard claims of an ID token and
// returns its payload.
func (p *Provider) verify(ctx context.Context, token, clientID string) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("ID-Token ungültig")
	}
	head, err1 := b64(parts[0])
	payload, err2 := b64(parts[1])
	sig, err3 := b64(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("ID-Token ungültig")
	}
	var h struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(head, &h) != nil {
		return nil, errors.New("ID-Token ungültig")
	}
	keys, err := p.keySet(ctx, h.Kid)
	if err != nil {
		return nil, err
	}
	var candidates []jwk
	if k, ok := keys[h.Kid]; ok {
		candidates = append(candidates, k)
	} else if h.Kid == "" {
		for _, k := range keys {
			candidates = append(candidates, k)
		}
	}
	signed := []byte(parts[0] + "." + parts[1])
	ok := false
	for _, k := range candidates {
		if k.Alg != "" && k.Alg != h.Alg {
			continue
		}
		if checkSignature(h.Alg, k, signed, sig) == nil {
			ok = true
			break
		}
	}
	if !ok {
		return nil, fmt.Errorf("Signatur des ID-Tokens ungültig (%s)", h.Alg)
	}

	var c struct {
		Iss string          `json:"iss"`
		Aud json.RawMessage `json:"aud"`
		Azp string          `json:"azp"`
		Exp float64         `json:"exp"`
		Iat float64         `json:"iat"`
	}
	if json.Unmarshal(payload, &c) != nil {
		return nil, errors.New("ID-Token ungültig")
	}
	if strings.TrimRight(c.Iss, "/") != strings.TrimRight(p.Issuer, "/") {
		return nil, errors.New("ID-Token von einem anderen Anbieter")
	}
	var aud []string
	if json.Unmarshal(c.Aud, &aud) != nil {
		var one string
		json.Unmarshal(c.Aud, &one)
		aud = []string{one}
	}
	if !slices.Contains(aud, clientID) || (len(aud) > 1 && c.Azp != "" && c.Azp != clientID) {
		return nil, errors.New("ID-Token ist nicht für Heimdeck ausgestellt")
	}
	now := time.Now()
	const skew = 2 * time.Minute
	if c.Exp == 0 || now.After(time.Unix(int64(c.Exp), 0).Add(skew)) {
		return nil, errors.New("ID-Token abgelaufen – Uhrzeit von Heimdeck und Anbieter prüfen")
	}
	if c.Iat != 0 && time.Unix(int64(c.Iat), 0).After(now.Add(skew)) {
		return nil, errors.New("ID-Token aus der Zukunft – Uhrzeit von Heimdeck und Anbieter prüfen")
	}
	return payload, nil
}

func checkSignature(alg string, k jwk, signed, sig []byte) error {
	if len(alg) < 5 { // "none", "" – never accepted
		return fmt.Errorf("unsupported algorithm %q", alg)
	}
	var h hash.Hash
	var ch crypto.Hash
	switch alg[len(alg)-3:] {
	case "256":
		h, ch = sha256.New(), crypto.SHA256
	case "384":
		h, ch = sha512.New384(), crypto.SHA384
	case "512":
		h, ch = sha512.New(), crypto.SHA512
	}
	switch {
	case (strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS")) && h != nil && k.Kty == "RSA":
		n, err1 := b64(k.N)
		e, err2 := b64(k.E)
		if err1 != nil || err2 != nil {
			return errors.New("bad key")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < 2048 {
			return errors.New("key too short")
		}
		h.Write(signed)
		if alg[0] == 'R' {
			return rsa.VerifyPKCS1v15(pub, ch, h.Sum(nil), sig)
		}
		return rsa.VerifyPSS(pub, ch, h.Sum(nil), sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	case strings.HasPrefix(alg, "ES") && h != nil && k.Kty == "EC":
		curve := map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()}[k.Crv]
		x, err1 := b64(k.X)
		y, err2 := b64(k.Y)
		if curve == nil || err1 != nil || err2 != nil || len(sig)%2 != 0 {
			return errors.New("bad key")
		}
		pub := &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		h.Write(signed)
		r, s := new(big.Int).SetBytes(sig[:len(sig)/2]), new(big.Int).SetBytes(sig[len(sig)/2:])
		if !ecdsa.Verify(pub, h.Sum(nil), r, s) {
			return errors.New("bad signature")
		}
		return nil
	case alg == "EdDSA" && k.Kty == "OKP" && k.Crv == "Ed25519":
		x, err := b64(k.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return errors.New("bad key")
		}
		if !ed25519.Verify(ed25519.PublicKey(x), signed, sig) {
			return errors.New("bad signature")
		}
		return nil
	}
	// "none", HS256 (shared secret) and anything unknown are refused.
	return fmt.Errorf("unsupported algorithm %s", alg)
}
