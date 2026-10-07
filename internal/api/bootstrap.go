package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"herdr-space/internal/auth"
	"herdr-space/internal/store"
)

type setupPending struct {
	Prepared       auth.PreparedEnrollment
	Email, Subject string
	CSRFHash       [32]byte
	Expires        time.Time
	Attempts       int
	InUse          bool
}

func setupRandom() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func (s *Server) setupAvailable(r *http.Request) bool {
	if s.bootstrapVerify == nil {
		return false
	}
	_, e := s.bootstrapVerify(r.Context(), r)
	return e == nil
}
func (s *Server) setup(w http.ResponseWriter, r *http.Request, route string) {
	if r.Method != "POST" || s.bootstrapVerify == nil {
		failure(w, 404)
		return
	}
	configured, _, e := s.Auth.Configured(r.Context())
	if e != nil {
		failure(w, 503)
		return
	}
	if configured {
		failure(w, 404)
		return
	}
	if !s.originOK(r) {
		failure(w, 403)
		return
	}
	identity, e := s.bootstrapVerify(r.Context(), r)
	if e != nil || identity.Subject == "" || identity.Email == "" {
		failure(w, 403)
		return
	}
	if !s.rateLimit(r) {
		failure(w, 429)
		return
	}
	switch route {
	case "auth/setup/begin":
		s.setupBegin(w, r, identity)
	case "auth/setup/complete":
		s.setupComplete(w, r, identity)
	default:
		failure(w, 404)
	}
}
func (s *Server) setupBegin(w http.ResponseWriter, r *http.Request, identity auth.AccessIdentity) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if decode(r, &in) != nil {
		failure(w, 400)
		return
	}
	prepared, e := auth.PrepareEnrollment(in.Username, in.Password)
	if e != nil {
		resultErr(w, e)
		return
	}
	token, csrf := setupRandom(), setupRandom()
	tokenHash := sha256.Sum256([]byte(token))
	csrfHash := sha256.Sum256([]byte(csrf))
	now := time.Now()
	s.mu.Lock()
	for key, pending := range s.pending {
		if now.After(pending.Expires) || pending.Subject == identity.Subject && pending.Email == identity.Email {
			delete(s.pending, key)
		}
	}
	if len(s.pending) >= 8 {
		s.mu.Unlock()
		failure(w, 429)
		return
	}
	s.pending[tokenHash] = setupPending{Prepared: prepared, Email: identity.Email, Subject: identity.Subject, CSRFHash: csrfHash, Expires: now.Add(10 * time.Minute)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "herdr_setup", Value: token, Path: "/api/v1/auth/setup/", HttpOnly: true, Secure: !s.C.AllowInsecureLocal, SameSite: http.SameSiteStrictMode, MaxAge: 600})
	label := "HERDR Space:" + prepared.Username
	query := url.Values{"secret": {prepared.Secret}, "issuer": {"HERDR Space"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	jsonout(w, 200, map[string]string{"secret": prepared.Secret, "otpauth_url": "otpauth://totp/" + url.PathEscape(label) + "?" + query.Encode(), "csrf_token": csrf})
}
func (s *Server) clearSetupCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "herdr_setup", Value: "", Path: "/api/v1/auth/setup/", HttpOnly: true, Secure: !s.C.AllowInsecureLocal, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
func (s *Server) setupComplete(w http.ResponseWriter, r *http.Request, identity auth.AccessIdentity) {
	var in struct {
		OTP  string `json:"otp"`
		CSRF string `json:"csrf"`
	}
	if decode(r, &in) != nil {
		failure(w, 400)
		return
	}
	cookie, e := r.Cookie("herdr_setup")
	if e != nil || len(cookie.Value) != 64 || len(in.CSRF) > 128 {
		failure(w, 403)
		return
	}
	key := sha256.Sum256([]byte(cookie.Value))
	csrfHash := sha256.Sum256([]byte(in.CSRF))
	s.mu.Lock()
	pending, ok := s.pending[key]
	if !ok {
		s.mu.Unlock()
		s.clearSetupCookie(w)
		failure(w, 410)
		return
	}
	if time.Now().After(pending.Expires) {
		delete(s.pending, key)
		s.mu.Unlock()
		s.clearSetupCookie(w)
		failure(w, 410)
		return
	}
	if pending.Subject != identity.Subject || pending.Email != identity.Email {
		delete(s.pending, key)
		s.mu.Unlock()
		s.clearSetupCookie(w)
		failure(w, 403)
		return
	}
	if subtle.ConstantTimeCompare(csrfHash[:], pending.CSRFHash[:]) != 1 {
		s.mu.Unlock()
		failure(w, 403)
		return
	}
	if pending.InUse {
		s.mu.Unlock()
		failure(w, 409)
		return
	}
	pending.InUse = true
	s.pending[key] = pending
	s.mu.Unlock()
	codes, enrollErr := s.Auth.EnrollPrepared(r.Context(), pending.Prepared, strings.TrimSpace(in.OTP), time.Now())
	s.mu.Lock()
	current, still := s.pending[key]
	exhausted := false
	if still {
		if enrollErr == nil || !errors.Is(enrollErr, auth.ErrDenied) {
			delete(s.pending, key)
		} else {
			current.Attempts++
			exhausted = current.Attempts >= 5
			if exhausted {
				delete(s.pending, key)
			} else {
				current.InUse = false
				s.pending[key] = current
			}
		}
	}
	s.mu.Unlock()
	if enrollErr != nil {
		if errors.Is(enrollErr, auth.ErrDenied) {
			if exhausted {
				s.clearSetupCookie(w)
				failure(w, 410)
			} else {
				failure(w, 400)
			}
			return
		}
		s.clearSetupCookie(w)
		configured, _, checkErr := s.Auth.Configured(r.Context())
		if checkErr == nil && configured {
			failure(w, 404)
			return
		}
		if errors.Is(enrollErr, store.ErrInvalid) {
			failure(w, 400)
			return
		}
		failure(w, 503)
		return
	}
	s.clearSetupCookie(w)
	jsonout(w, 200, map[string]any{"username": pending.Prepared.Username, "recovery_codes": codes, "configured": true})
}
