package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func signedAccessJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	return signedAccessJWTWithHeader(t, key, map[string]string{"alg": "RS256", "kid": "key-1", "typ": "JWT"}, claims)
}
func signedAccessJWTWithHeader(t *testing.T, key *rsa.PrivateKey, header map[string]string, claims map[string]any) string {
	t.Helper()
	head, _ := json.Marshal(header)
	body, _ := json.Marshal(claims)
	message := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(message))
	sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if e != nil {
		t.Fatal(e)
	}
	return message + "." + base64.RawURLEncoding.EncodeToString(sig)
}
func TestAccessJWTAllowsOptionalTypeHeader(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	verifier, e := NewAccessVerifier("https://test.cloudflareaccess.com", "the-audience", "owner@example.test")
	if e != nil {
		t.Fatal(e)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kid": "key-1", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
	verifier.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(jwks))), Header: make(http.Header)}, nil
	})}
	now := time.Now().Unix()
	claims := map[string]any{"iss": "https://test.cloudflareaccess.com", "aud": []string{"the-audience"}, "email": "owner@example.test", "sub": "user-1", "type": "app", "iat": now - 1, "nbf": now - 1, "exp": now + 300}
	for _, tc := range []struct {
		name   string
		header map[string]string
	}{
		{"typ absent", map[string]string{"alg": "RS256", "kid": "key-1"}},
		{"typ lowercase", map[string]string{"alg": "RS256", "kid": "key-1", "typ": "jwt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity, err := verifier.VerifyJWT(context.Background(), signedAccessJWTWithHeader(t, key, tc.header, claims))
			if err != nil || identity.Email != "owner@example.test" || identity.Subject != "user-1" {
				t.Fatal("valid signed Access JWT rejected")
			}
		})
	}
}
func TestAccessJWTRequiresSignatureAudienceEmailAndTime(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	verifier, e := NewAccessVerifier("https://test.cloudflareaccess.com", "the-audience", "owner@example.test")
	if e != nil {
		t.Fatal(e)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kid": "key-1", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
	calls := 0
	verifier.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://test.cloudflareaccess.com/cdn-cgi/access/certs" {
			t.Fatalf("unexpected JWKS URL %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(jwks))), Header: make(http.Header)}, nil
	})}
	now := time.Now()
	claims := map[string]any{"iss": "https://test.cloudflareaccess.com", "aud": []string{"the-audience"}, "email": "owner@example.test", "sub": "user-1", "type": "app", "iat": now.Unix() - 1, "nbf": now.Unix() - 1, "exp": now.Unix() + 300}
	token := signedAccessJWT(t, key, claims)
	identity, e := verifier.VerifyJWT(context.Background(), token)
	if e != nil || identity.Email != "owner@example.test" || identity.Subject != "user-1" {
		t.Fatalf("valid identity %+v %v", identity, e)
	}
	if calls != 1 {
		t.Fatalf("JWKS fetches %d", calls)
	}
	unknownHeader, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "unknown", "typ": "JWT"})
	unknownToken := base64.RawURLEncoding.EncodeToString(unknownHeader) + "." + base64.RawURLEncoding.EncodeToString([]byte(`{}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte("bad"))
	for i := 0; i < 2; i++ {
		if _, e := verifier.VerifyJWT(context.Background(), unknownToken); e == nil {
			t.Fatal("unknown key accepted")
		}
	}
	if calls != 1 {
		t.Fatalf("unknown kid amplified JWKS fetches to %d", calls)
	}
	bad := map[string]any{}
	for k, v := range claims {
		bad[k] = v
	}
	bad["aud"] = []string{"wrong"}
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, key, bad)); e == nil {
		t.Fatal("wrong audience accepted")
	}
	bad["aud"] = claims["aud"]
	bad["email"] = "other@example.test"
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, key, bad)); e == nil {
		t.Fatal("wrong email accepted")
	}
	bad["email"] = claims["email"]
	bad["exp"] = now.Unix() - 10
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, key, bad)); e == nil {
		t.Fatal("expired token accepted")
	}
	bad["exp"] = claims["exp"]
	bad["nbf"] = now.Unix() + 300
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, key, bad)); e == nil {
		t.Fatal("future token accepted")
	}
	bad["nbf"] = claims["nbf"]
	bad["sub"] = ""
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, key, bad)); e == nil {
		t.Fatal("service identity accepted")
	}
	bad["sub"] = claims["sub"]
	bad["type"] = "org"
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, key, bad)); e == nil {
		t.Fatal("global session token accepted")
	}
	other, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, other, claims)); e == nil {
		t.Fatal("wrong signature accepted")
	}
}
func TestAccessIssuerMustBeCloudflareTeamHTTPS(t *testing.T) {
	for _, issuer := range []string{"http://test.cloudflareaccess.com", "https://evil.example.com", "https://test.cloudflareaccess.com:8443", "https://test.cloudflareaccess.com/path"} {
		if _, e := NewAccessVerifier(issuer, "aud", "owner@example.test"); e == nil {
			t.Fatalf("accepted %s", issuer)
		}
	}
}
func TestAccessAllowsOnlyExplicitOwnerEmails(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	verifier, e := NewAccessVerifier("https://test.cloudflareaccess.com", "aud", "first@example.test,second@example.test")
	if e != nil {
		t.Fatal(e)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kid": "key-1", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
	verifier.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(jwks))), Header: make(http.Header)}, nil
	})}
	now := time.Now().Unix()
	claims := map[string]any{"iss": "https://test.cloudflareaccess.com", "aud": []string{"aud"}, "email": "second@example.test", "sub": "same-sub", "type": "app", "iat": now - 1, "nbf": now - 1, "exp": now + 300}
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, key, claims)); e != nil {
		t.Fatal(e)
	}
	claims["email"] = "third@example.test"
	if _, e := verifier.VerifyJWT(context.Background(), signedAccessJWT(t, key, claims)); e == nil {
		t.Fatal("domain-only email accepted")
	}
}
