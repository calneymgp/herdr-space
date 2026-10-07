package auth

import (
	"context"
	"crypto/sha256"
	"herdr-space/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfiguredDatabaseNeverRegeneratesMissingAuthKey(t *testing.T) {
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	keyPath := filepath.Join(dir, "auth.key")
	a, e := New(s, keyPath)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.Setup(context.Background(), "admin", "correct horse battery staple"); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(keyPath); e != nil {
		t.Fatal(e)
	}
	if _, e = New(s, keyPath); e == nil {
		t.Fatal("configured database accepted a replacement encryption key")
	}
	if _, e = os.Stat(keyPath); !os.IsNotExist(e) {
		t.Fatal("missing key was recreated")
	}
}

func TestCorruptAuthenticatorCipherDataFailsWithoutPanic(t *testing.T) {
	dir := t.TempDir()
	s, e := store.Open(filepath.Join(dir, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := New(s, filepath.Join(dir, "auth.key"))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.Setup(context.Background(), "admin", "correct horse battery staple"); e != nil {
		t.Fatal(e)
	}
	var goodNonce, goodCipher []byte
	if e = s.DB.QueryRow("SELECT totp_nonce,totp_cipher FROM admin WHERE id=1").Scan(&goodNonce, &goodCipher); e != nil {
		t.Fatal(e)
	}
	for _, variant := range []struct {
		name   string
		nonce  []byte
		cipher []byte
	}{
		{name: "short nonce", nonce: []byte{1}, cipher: goodCipher},
		{name: "long nonce", nonce: make([]byte, len(goodNonce)+1), cipher: goodCipher},
		{name: "bad ciphertext", nonce: goodNonce, cipher: []byte{1}},
	} {
		t.Run(variant.name, func(t *testing.T) {
			if _, e := s.DB.Exec("UPDATE admin SET totp_nonce=?,totp_cipher=? WHERE id=1", variant.nonce, variant.cipher); e != nil {
				t.Fatal(e)
			}
			if _, _, e := a.Login(context.Background(), "admin", "correct horse battery staple", "000000", time.Now()); e == nil {
				t.Fatal("login accepted corrupt authenticator data")
			}
			if e := a.VerifyBackupKey(context.Background()); e == nil {
				t.Fatal("backup verification accepted corrupt authenticator data")
			}
		})
	}
}

func TestBothFactorsReplayAndRecovery(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := New(s, filepath.Join(t.TempDir(), "key"))
	if e != nil {
		t.Fatal(e)
	}
	secret, codes, e := a.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now().UTC()
	otp := Code(secret, at)
	if _, _, e = a.Login(context.Background(), "admin", "correct horse battery staple", "", at); e == nil {
		t.Fatal("password-only login")
	}
	if _, _, e = a.Login(context.Background(), "admin", "wrong", otp, at); e == nil {
		t.Fatal("wrong password")
	}
	token, csrf, e := a.Login(context.Background(), "admin", "correct horse battery staple", otp, at)
	if e != nil {
		t.Fatal(e)
	}
	if token == "" || csrf == "" {
		t.Fatal("empty credentials")
	}
	if _, _, e = a.Login(context.Background(), "admin", "correct horse battery staple", otp, at); e == nil {
		t.Fatal("TOTP replay")
	}
	if _, _, e = a.Login(context.Background(), "admin", "correct horse battery staple", codes[0], at); e != nil {
		t.Fatal(e)
	}
	if _, _, e = a.Login(context.Background(), "admin", "correct horse battery staple", codes[0], at); e == nil {
		t.Fatal("recovery replay")
	}
	if _, e = a.Validate(context.Background(), token, at.Add(25*time.Hour)); e != nil {
		t.Fatal("valid session expired after one day")
	}
}
func TestSessionHasAbsoluteSevenDayLifetimeWithoutIdleExpiry(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := New(s, filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := a.Setup(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	wantLifetime := 7 * 24 * time.Hour
	token, _, err := a.Login(context.Background(), "admin", "correct horse battery staple", Code(secret, at), at)
	if err != nil {
		t.Fatal(err)
	}
	for _, elapsed := range []time.Duration{2 * time.Hour, 6 * 24 * time.Hour, wantLifetime - time.Second} {
		session, err := a.Validate(context.Background(), token, at.Add(elapsed))
		if err != nil {
			t.Fatalf("valid after %s: %v", elapsed, err)
		}
		if !session.ExpiresAt.Equal(at.Add(wantLifetime)) {
			t.Fatalf("expiry changed: %s", session.ExpiresAt)
		}
	}
	if err := a.Touch(context.Background(), token, at.Add(6*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Validate(context.Background(), token, at.Add(wantLifetime)); err == nil {
		t.Fatal("absolute expiry ignored after Touch")
	}
	if _, err := a.Validate(context.Background(), token, at.Add(wantLifetime+time.Second)); err == nil {
		t.Fatal("expired session revived")
	}
}

func TestLegacySessionUpgradesOnlyWhileOriginallyValid(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := New(s, filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	secret, recovery, err := a.Setup(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	issue := func(otp string, idle time.Duration) string {
		t.Helper()
		token, _, err := a.Login(context.Background(), "admin", "correct horse battery staple", otp, at)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(token))
		_, err = s.DB.Exec("UPDATE sessions SET expires_at=?,seen_at=? WHERE token_hash=?", at.Add(24*time.Hour).Format(time.RFC3339Nano), at.Add(idle).Format(time.RFC3339Nano), hash[:])
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	valid := issue(Code(secret, at), 0)
	session, err := a.Validate(context.Background(), valid, at.Add(30*time.Minute))
	if err != nil || !session.ExpiresAt.Equal(at.Add(7*24*time.Hour)) {
		t.Fatalf("valid legacy not upgraded: %v", err)
	}
	if _, err := a.Validate(context.Background(), valid, at.Add(6*24*time.Hour)); err != nil {
		t.Fatalf("upgraded session expired: %v", err)
	}
	if _, err := a.Validate(context.Background(), valid, at.Add(7*24*time.Hour)); err == nil {
		t.Fatal("upgraded legacy exceeded absolute lifetime")
	}
	idle := issue(recovery[0], 0)
	if _, err := a.Validate(context.Background(), idle, at.Add(2*time.Hour)); err == nil {
		t.Fatal("legacy idle expiry revived")
	}
	absExpired := issue(recovery[1], 0)
	if _, err := a.Validate(context.Background(), absExpired, at.Add(25*time.Hour)); err == nil {
		t.Fatal("legacy absolute expiry revived")
	}
	revoked := issue(recovery[2], 0)
	if err := a.Logout(context.Background(), revoked); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Validate(context.Background(), revoked, at.Add(30*time.Minute)); err == nil {
		t.Fatal("revoked legacy revived")
	}
	futureSeen := issue(recovery[3], time.Hour)
	if _, err := a.Validate(context.Background(), futureSeen, at.Add(30*time.Minute)); err == nil {
		t.Fatal("legacy with future seen_at upgraded")
	}
}
func TestSessionRejectsCreationTimestampInFuture(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := New(s, filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := a.Setup(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	token, _, err := a.Login(context.Background(), "admin", "correct horse battery staple", Code(secret, at), at)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(token))
	if _, err := s.DB.Exec("UPDATE sessions SET created_at=? WHERE token_hash=?", at.Add(time.Hour).Format(time.RFC3339Nano), hash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Validate(context.Background(), token, at.Add(30*time.Minute)); err == nil {
		t.Fatal("future creation timestamp accepted")
	}
}
func TestStandardTOTP(t *testing.T) {
	if got := Code("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", time.Unix(59, 0)); got != "287082" {
		t.Fatalf("got %s", got)
	}
}
func TestEnrollmentRequiresCurrentCode(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := New(s, filepath.Join(t.TempDir(), "key"))
	if e != nil {
		t.Fatal(e)
	}
	secret := GenerateSecret()
	if _, e := a.Enroll(context.Background(), "admin", "correct horse battery staple", secret, "000000"); e == nil {
		t.Fatal("invalid enrollment code accepted")
	}
	ok, _, e := a.Configured(context.Background())
	if e != nil || ok {
		t.Fatalf("configured after failed code: %v %v", ok, e)
	}
	if _, e := a.Enroll(context.Background(), "admin", "correct horse battery staple", secret, Code(secret, time.Now())); e != nil {
		t.Fatal(e)
	}
}
func TestSessionSurvivesIdleAndLogoutRevokes(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := New(s, filepath.Join(t.TempDir(), "key"))
	if e != nil {
		t.Fatal(e)
	}
	secret, _, e := a.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now()
	token, csrf, e := a.Login(context.Background(), "admin", "correct horse battery staple", Code(secret, at), at)
	if e != nil {
		t.Fatal(e)
	}
	session, e := a.Validate(context.Background(), token, at.Add(59*time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	if !a.CSRF(session, csrf) || a.CSRF(session, "wrong") {
		t.Fatal("CSRF check")
	}
	if _, e := a.Validate(context.Background(), token, at.Add(120*time.Minute)); e != nil {
		t.Fatal("idle session expired")
	}
	if e := a.Logout(context.Background(), token); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Validate(context.Background(), token, at.Add(2*time.Minute)); e == nil {
		t.Fatal("logout")
	}
}
func TestValidationDoesNotExtendAbsoluteWindow(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := New(s, filepath.Join(t.TempDir(), "key"))
	if e != nil {
		t.Fatal(e)
	}
	secret, _, e := a.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now()
	token, _, e := a.Login(context.Background(), "admin", "correct horse battery staple", Code(secret, at), at)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := a.Validate(context.Background(), token, at.Add(6*24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Validate(context.Background(), token, at.Add(7*24*time.Hour)); e == nil {
		t.Fatal("validation heartbeat extended absolute expiry")
	}
}
func TestLoginWritesMinimalAuditEvent(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := New(s, filepath.Join(t.TempDir(), "key"))
	if e != nil {
		t.Fatal(e)
	}
	secret, _, e := a.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	token, _, e := a.Login(context.Background(), "admin", "correct horse battery staple", Code(secret, time.Now()), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	var event string
	if e := s.DB.QueryRow("SELECT event FROM audit ORDER BY created_at DESC LIMIT 1").Scan(&event); e != nil {
		t.Fatal(e)
	}
	if event != "login" || strings.Contains(event, token) {
		t.Fatalf("audit event %q", event)
	}
}
