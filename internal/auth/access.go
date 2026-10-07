package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type AccessIdentity struct{ Email, Subject string }
type AccessVerifier struct {
	issuer, audience string
	emails           map[string]struct{}
	client           *http.Client
	mu               sync.Mutex
	keys             map[string]*rsa.PublicKey
	fetchedAt        time.Time
	lastFetch        time.Time
}

func NewAccessVerifier(issuer, audience, email string) (*AccessVerifier, error) {
	u, e := url.Parse(issuer)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "" || !strings.HasSuffix(u.Hostname(), ".cloudflareaccess.com") || len(u.Hostname()) <= len(".cloudflareaccess.com") || strings.Contains(u.Hostname(), "_") || audience == "" || len(audience) > 256 {
		return nil, ErrDenied
	}
	emails := map[string]struct{}{}
	for _, entry := range strings.Split(email, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" || len(entry) > 320 || strings.Count(entry, "@") != 1 || strings.ContainsAny(entry, " \t\r\n/*\\") || len(emails) >= 8 {
			return nil, ErrDenied
		}
		emails[entry] = struct{}{}
	}
	return &AccessVerifier{issuer: issuer, audience: audience, emails: emails, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// NewAccessVerifierWithClient permits a controlled transport for integration
// tests while retaining the fixed issuer-derived JWKS URL and redirect ban.
func NewAccessVerifierWithClient(issuer, audience, email string, client *http.Client) (*AccessVerifier, error) {
	v, err := NewAccessVerifier(issuer, audience, email)
	if err != nil {
		return nil, err
	}
	if client != nil {
		copy := *client
		copy.Timeout = 5 * time.Second
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		v.client = &copy
	}
	return v, nil
}
func (v *AccessVerifier) VerifyRequest(ctx context.Context, r *http.Request) (AccessIdentity, error) {
	token := r.Header.Get("Cf-Access-Jwt-Assertion")
	if token == "" {
		if c, e := r.Cookie("CF_Authorization"); e == nil {
			token = c.Value
		}
	}
	return v.VerifyJWT(ctx, token)
}
func (v *AccessVerifier) VerifyJWT(ctx context.Context, token string) (AccessIdentity, error) {
	var identity AccessIdentity
	if len(token) > 8192 || len(token) < 32 {
		return identity, ErrDenied
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return identity, ErrDenied
	}
	headBytes, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || len(headBytes) > 1024 {
		return identity, ErrDenied
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(headBytes, &head) != nil || head.Alg != "RS256" || head.Kid == "" || len(head.Kid) > 128 {
		return identity, ErrDenied
	}
	key, e := v.key(ctx, head.Kid)
	if e != nil {
		return identity, ErrDenied
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil || len(sig) > 1024 {
		return identity, ErrDenied
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return identity, ErrDenied
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || len(payload) > 4096 {
		return identity, ErrDenied
	}
	var claims struct {
		Issuer    string   `json:"iss"`
		Audience  []string `json:"aud"`
		Email     string   `json:"email"`
		Subject   string   `json:"sub"`
		Type      string   `json:"type"`
		Expires   int64    `json:"exp"`
		NotBefore int64    `json:"nbf"`
		IssuedAt  int64    `json:"iat"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return identity, ErrDenied
	}
	now := time.Now().Unix()
	_, emailAllowed := v.emails[strings.ToLower(claims.Email)]
	if claims.Issuer != v.issuer || !emailAllowed || claims.Subject == "" || claims.Type != "app" || claims.Expires <= now || claims.NotBefore > now || claims.NotBefore <= 0 || claims.IssuedAt <= 0 || claims.IssuedAt > now+30 {
		return identity, ErrDenied
	}
	audOK := false
	for _, aud := range claims.Audience {
		if aud == v.audience {
			audOK = true
			break
		}
	}
	if !audOK {
		return identity, ErrDenied
	}
	return AccessIdentity{Email: strings.ToLower(claims.Email), Subject: claims.Subject}, nil
}
func (v *AccessVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.fetchedAt) < 5*time.Minute {
		if key := v.keys[kid]; key != nil {
			return key, nil
		}
	}
	if time.Since(v.lastFetch) < 30*time.Second {
		return nil, ErrDenied
	}
	v.lastFetch = time.Now()
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(fetchCtx, "GET", v.issuer+"/cdn-cgi/access/certs", nil)
	if e != nil {
		return nil, e
	}
	resp, e := v.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, ErrDenied
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if e != nil || len(body) > 1<<20 {
		return nil, ErrDenied
	}
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(body, &jwks) != nil || len(jwks.Keys) == 0 || len(jwks.Keys) > 8 {
		return nil, ErrDenied
	}
	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, j := range jwks.Keys {
		if j.Kid == "" || len(j.Kid) > 128 || j.Kty != "RSA" || j.Alg != "RS256" || j.Use != "sig" {
			return nil, ErrDenied
		}
		if _, exists := keys[j.Kid]; exists {
			return nil, ErrDenied
		}
		n, ne := base64.RawURLEncoding.DecodeString(j.N)
		ex, ee := base64.RawURLEncoding.DecodeString(j.E)
		if ne != nil || ee != nil || len(n) < 256 || len(n) > 1024 || len(ex) == 0 || len(ex) > 4 {
			return nil, ErrDenied
		}
		var exponent uint32
		for _, b := range ex {
			exponent = exponent<<8 | uint32(b)
		}
		if exponent < 3 || exponent%2 == 0 || exponent > 1<<31-1 {
			return nil, ErrDenied
		}
		keys[j.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent)}
	}
	v.keys = keys
	v.fetchedAt = time.Now()
	key := keys[kid]
	if key == nil {
		return nil, errors.New("unknown access signing key")
	}
	return key, nil
}
