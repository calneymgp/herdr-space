package auth

import (
	"context"
	"herdr-space/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparedEnrollmentStoresHashAndCommitsOnlyAfterOTP(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := New(s, filepath.Join(t.TempDir(), "key"))
	if e != nil {
		t.Fatal(e)
	}
	pending, e := PrepareEnrollment("admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(pending.PasswordHash, "correct horse battery staple") || pending.Secret == "" {
		t.Fatal("plaintext pending password or missing secret")
	}
	if _, e := a.EnrollPrepared(context.Background(), pending, "000000", time.Now()); e == nil {
		t.Fatal("invalid OTP enrolled")
	}
	configured, _, e := a.Configured(context.Background())
	if e != nil || configured {
		t.Fatalf("premature account %v %v", configured, e)
	}
	codes, e := a.EnrollPrepared(context.Background(), pending, Code(pending.Secret, time.Now()), time.Now())
	if e != nil || len(codes) != 10 {
		t.Fatalf("complete %d %v", len(codes), e)
	}
	configured, _, e = a.Configured(context.Background())
	if e != nil || !configured {
		t.Fatalf("missing account %v %v", configured, e)
	}
}
func TestEnrollmentOTPIsConsumedAtomically(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := New(s, filepath.Join(t.TempDir(), "key"))
	if e != nil {
		t.Fatal(e)
	}
	pending, e := PrepareEnrollment("admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now()
	otp := Code(pending.Secret, at)
	recovery, e := a.EnrollPrepared(context.Background(), pending, otp, at)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e := a.Login(context.Background(), "admin", "correct horse battery staple", otp, at); e == nil {
		t.Fatal("enrollment code replayed at login")
	}
	if _, _, e := a.Login(context.Background(), "admin", "correct horse battery staple", recovery[0], at); e != nil {
		t.Fatalf("recovery login %v", e)
	}
}
