package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"herdr-space/internal/store"
)

var ErrDenied = errors.New("access denied")

const SessionLifetime = 7 * 24 * time.Hour

type Auth struct {
	S   *store.Store
	key []byte
}

func New(s *store.Store, keyPath string) (*Auth, error) {
	if info, err := os.Lstat(keyPath); err == nil {
		if !info.Mode().IsRegular() {
			return nil, store.ErrInvalid
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	b, e := os.ReadFile(keyPath)
	if errors.Is(e, os.ErrNotExist) {
		configured, _, checkErr := (&Auth{S: s}).Configured(context.Background())
		if checkErr != nil {
			return nil, checkErr
		}
		if configured {
			return nil, errors.New("authentication key missing for configured database")
		}
		b = make([]byte, 32)
		if _, e = rand.Read(b); e != nil {
			return nil, e
		}
		f, er := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if er != nil {
			return nil, er
		}
		_, e = f.Write(b)
		ce := f.Close()
		if e == nil {
			e = ce
		}
	}
	if e != nil {
		return nil, e
	}
	if len(b) != 32 {
		return nil, errors.New("invalid encryption key")
	}
	if e = os.Chmod(keyPath, 0600); e != nil {
		return nil, e
	}
	return &Auth{S: s, key: b}, nil
}
func random(n int) []byte {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return b
}
func hashPassword(p string) string {
	salt := random(16)
	h := argon2.IDKey([]byte(p), salt, 3, 64*1024, 4, 32)
	return fmt.Sprintf("%x:%x", salt, h)
}
func verifyPassword(stored, p string) bool {
	parts := strings.Split(stored, ":")
	if len(parts) != 2 {
		return false
	}
	salt, e := hex.DecodeString(parts[0])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := hex.DecodeString(parts[1])
	if e != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(p), salt, 3, 64*1024, 4, 32)
	return subtle.ConstantTimeCompare(want, got) == 1
}
func (a *Auth) encrypt(secret string) ([]byte, []byte, error) {
	block, e := aes.NewCipher(a.key)
	if e != nil {
		return nil, nil, e
	}
	g, e := cipher.NewGCM(block)
	if e != nil {
		return nil, nil, e
	}
	nonce := random(g.NonceSize())
	return nonce, g.Seal(nil, nonce, []byte(secret), nil), nil
}
func (a *Auth) decrypt(nonce, ciphertext []byte) (string, error) {
	b, e := aes.NewCipher(a.key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	if len(nonce) != g.NonceSize() {
		return "", ErrDenied
	}
	v, e := g.Open(nil, nonce, ciphertext, nil)
	return string(v), e
}
func Code(secret string, at time.Time) string {
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if e != nil {
		return ""
	}
	var c [8]byte
	binary.BigEndian.PutUint64(c[:], uint64(at.Unix()/30))
	m := hmac.New(sha1.New, key)
	m.Write(c[:])
	sum := m.Sum(nil)
	o := int(sum[len(sum)-1] & 15)
	v := binary.BigEndian.Uint32(sum[o:o+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}
func (a *Auth) Configured(ctx context.Context) (bool, string, error) {
	var u string
	e := a.S.DB.QueryRowContext(ctx, "SELECT username FROM admin WHERE id=1").Scan(&u)
	if errors.Is(e, sql.ErrNoRows) {
		return false, "", nil
	}
	return e == nil, u, e
}
func validPass(p string) bool { return len(p) >= 16 && len(p) <= 1024 }
func GenerateSecret() string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random(20))
}

type PreparedEnrollment struct {
	Username     string
	PasswordHash string
	Secret       string
}

func PrepareEnrollment(username, password string) (PreparedEnrollment, error) {
	if strings.TrimSpace(username) == "" || len(username) > 64 || !validPass(password) {
		return PreparedEnrollment{}, store.ErrInvalid
	}
	return PreparedEnrollment{Username: username, PasswordHash: hashPassword(password), Secret: GenerateSecret()}, nil
}
func (a *Auth) EnrollPrepared(ctx context.Context, pending PreparedEnrollment, otp string, at time.Time) ([]string, error) {
	matchedStep, valid := matchTOTPStep(pending.Secret, otp, at)
	if !valid {
		return nil, ErrDenied
	}
	_, codes, err := a.setupSecretHash(ctx, pending.Username, pending.PasswordHash, pending.Secret, &matchedStep)
	return codes, err
}
func (a *Auth) Enroll(ctx context.Context, u, p, secret, otp string) ([]string, error) {
	matchedStep, valid := matchTOTPStep(secret, otp, time.Now())
	if !valid {
		return nil, ErrDenied
	}
	_, codes, e := a.setupSecretWithStep(ctx, u, p, secret, &matchedStep)
	return codes, e
}
func matchTOTPStep(secret, otp string, at time.Time) (int64, bool) {
	for offset := -1; offset <= 1; offset++ {
		step := at.Unix()/30 + int64(offset)
		candidate := Code(secret, time.Unix(step*30, 0))
		if len(otp) == 6 && candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(otp)) == 1 {
			return step, true
		}
	}
	return 0, false
}
func (a *Auth) Setup(ctx context.Context, u, p string) (string, []string, error) {
	return a.setupSecret(ctx, u, p, GenerateSecret())
}
func (a *Auth) setupSecret(ctx context.Context, u, p, secret string) (string, []string, error) {
	return a.setupSecretWithStep(ctx, u, p, secret, nil)
}
func (a *Auth) setupSecretWithStep(ctx context.Context, u, p, secret string, usedStep *int64) (string, []string, error) {
	if strings.TrimSpace(u) == "" || len(u) > 64 || !validPass(p) {
		return "", nil, store.ErrInvalid
	}
	return a.setupSecretHash(ctx, u, hashPassword(p), secret, usedStep)
}
func (a *Auth) setupSecretHash(ctx context.Context, u, passwordHash, secret string, usedStep *int64) (string, []string, error) {
	if strings.TrimSpace(u) == "" || len(u) > 64 || passwordHash == "" || len(passwordHash) > 256 || Code(secret, time.Now()) == "" {
		return "", nil, store.ErrInvalid
	}
	ok, _, e := a.Configured(ctx)
	if e != nil {
		return "", nil, e
	}
	if ok {
		return "", nil, store.ErrConflict
	}
	nonce, ct, e := a.encrypt(secret)
	if e != nil {
		return "", nil, e
	}
	tx, e := a.S.DB.BeginTx(ctx, nil)
	if e != nil {
		return "", nil, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO admin VALUES(1,?,?,?,?)", u, passwordHash, nonce, ct); e != nil {
		return "", nil, e
	}
	if usedStep != nil {
		if _, e = tx.ExecContext(ctx, "INSERT INTO totp_steps(step,used_at) VALUES(?,?)", *usedStep, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
			return "", nil, e
		}
	}
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = strings.ToUpper(hex.EncodeToString(random(8)))
		h := sha256.Sum256([]byte(codes[i]))
		if _, e = tx.ExecContext(ctx, "INSERT INTO recovery(code_hash) VALUES(?)", h[:]); e != nil {
			return "", nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return "", nil, e
	}
	return secret, codes, nil
}
func (a *Auth) Login(ctx context.Context, u, p, otp string, at time.Time) (string, string, error) {
	var username, ph string
	var nonce, ct []byte
	e := a.S.DB.QueryRowContext(ctx, "SELECT username,password_hash,totp_nonce,totp_cipher FROM admin WHERE id=1").Scan(&username, &ph, &nonce, &ct)
	if e != nil {
		return "", "", ErrDenied
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(u)) != 1 || !verifyPassword(ph, p) {
		return "", "", ErrDenied
	}
	secret, e := a.decrypt(nonce, ct)
	if e != nil {
		return "", "", ErrDenied
	}
	tx, e := a.S.DB.BeginTx(ctx, nil)
	if e != nil {
		return "", "", e
	}
	defer tx.Rollback()
	accepted := false
	for offset := -1; offset <= 1; offset++ {
		step := at.Unix()/30 + int64(offset)
		candidate := Code(secret, time.Unix(step*30, 0))
		if candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(strings.TrimSpace(otp))) == 1 {
			_, e = tx.ExecContext(ctx, "INSERT INTO totp_steps(step,used_at) VALUES(?,?)", step, at.UTC().Format(time.RFC3339Nano))
			if e == nil {
				accepted = true
			}
			break
		}
	}
	if !accepted {
		h := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(otp))))
		r, er := tx.ExecContext(ctx, "UPDATE recovery SET used_at=? WHERE code_hash=? AND used_at IS NULL", at.UTC().Format(time.RFC3339Nano), h[:])
		if er == nil {
			n, _ := r.RowsAffected()
			accepted = n == 1
		}
	}
	if !accepted {
		return "", "", ErrDenied
	}
	token := hex.EncodeToString(random(32))
	csrf := a.TokenCSRF(token)
	th := sha256.Sum256([]byte(token))
	ch := sha256.Sum256([]byte(csrf))
	stamp := at.UTC().Format(time.RFC3339Nano)
	expiry := at.Add(SessionLifetime).UTC().Format(time.RFC3339Nano)
	if _, e = tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,?,?,?,NULL)", th[:], ch[:], stamp, stamp, expiry); e != nil {
		return "", "", e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM totp_steps WHERE step<?", at.Unix()/30-100); e != nil {
		return "", "", e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO audit(id,event,created_at) VALUES(?,?,?)", store.ID(), "login", stamp); e != nil {
		return "", "", e
	}
	if e = tx.Commit(); e != nil {
		return "", "", e
	}
	return token, csrf, nil
}

type Session struct {
	TokenHash []byte
	CSRFHash  []byte
	Username  string
	ExpiresAt time.Time
}

func (a *Auth) Validate(ctx context.Context, token string, at time.Time) (Session, error) {
	h := sha256.Sum256([]byte(token))
	for attempt := 0; attempt < 3; attempt++ {
		var x Session
		var created, seen, expires string
		e := a.S.DB.QueryRowContext(ctx, "SELECT csrf_hash,created_at,seen_at,expires_at FROM sessions WHERE token_hash=? AND revoked_at IS NULL", h[:]).Scan(&x.CSRFHash, &created, &seen, &expires)
		if e != nil {
			return x, ErrDenied
		}
		createdAt, e := time.Parse(time.RFC3339Nano, created)
		if e != nil {
			return x, ErrDenied
		}
		x.ExpiresAt, e = time.Parse(time.RFC3339Nano, expires)
		if e != nil || createdAt.After(at) || !at.Before(x.ExpiresAt) || x.ExpiresAt.After(createdAt.Add(SessionLifetime)) {
			return x, ErrDenied
		}
		duration := x.ExpiresAt.Sub(createdAt)
		legacy := duration >= 24*time.Hour-time.Minute && duration <= 24*time.Hour+time.Minute
		if !legacy {
			x.TokenHash = h[:]
			_, x.Username, e = a.Configured(ctx)
			return x, e
		}
		seenAt, e := time.Parse(time.RFC3339Nano, seen)
		if e != nil || seenAt.Before(createdAt) || seenAt.After(at) || at.Sub(seenAt) > time.Hour {
			return x, ErrDenied
		}
		nextExpiry := createdAt.Add(SessionLifetime).UTC().Format(time.RFC3339Nano)
		_, e = a.S.DB.ExecContext(ctx, "UPDATE sessions SET expires_at=? WHERE token_hash=? AND revoked_at IS NULL AND created_at=? AND seen_at=? AND expires_at=?", nextExpiry, h[:], created, seen, expires)
		if e != nil {
			return x, ErrDenied
		}
		// Re-read after the conditional update so a concurrent logout or upgrade wins.
	}
	return Session{}, ErrDenied
}
func (a *Auth) Touch(ctx context.Context, token string, at time.Time) error {
	if _, e := a.Validate(ctx, token, at); e != nil {
		return e
	}
	h := sha256.Sum256([]byte(token))
	r, e := a.S.DB.ExecContext(ctx, "UPDATE sessions SET seen_at=? WHERE token_hash=? AND revoked_at IS NULL", at.UTC().Format(time.RFC3339Nano), h[:])
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil || n != 1 {
		return ErrDenied
	}
	return nil
}
func (a *Auth) CSRF(session Session, token string) bool {
	h := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(h[:], session.CSRFHash) == 1
}
func (a *Auth) TokenCSRF(token string) string {
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte("csrf:"))
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}
func (a *Auth) Logout(ctx context.Context, token string) error {
	h := sha256.Sum256([]byte(token))
	tx, e := a.S.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	r, e := tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE token_hash=? AND revoked_at IS NULL", stamp, h[:])
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n == 1 {
		if _, e = tx.ExecContext(ctx, "INSERT INTO audit(id,event,created_at) VALUES(?,?,?)", store.ID(), "logout", stamp); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (a *Auth) Change(ctx context.Context, oldPass, otp, newPass string) (string, []string, error) {
	if !validPass(newPass) {
		return "", nil, store.ErrInvalid
	}
	var username string
	e := a.S.DB.QueryRowContext(ctx, "SELECT username FROM admin WHERE id=1").Scan(&username)
	if e != nil {
		return "", nil, e
	}
	_, _, e = a.Login(ctx, username, oldPass, otp, time.Now())
	if e != nil {
		return "", nil, e
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random(20))
	nonce, ct, e := a.encrypt(secret)
	if e != nil {
		return "", nil, e
	}
	tx, e := a.S.DB.BeginTx(ctx, nil)
	if e != nil {
		return "", nil, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE admin SET password_hash=?,totp_nonce=?,totp_cipher=? WHERE id=1", hashPassword(newPass), nonce, ct); e != nil {
		return "", nil, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM recovery"); e != nil {
		return "", nil, e
	}
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = strings.ToUpper(hex.EncodeToString(random(8)))
		h := sha256.Sum256([]byte(codes[i]))
		if _, e = tx.ExecContext(ctx, "INSERT INTO recovery(code_hash) VALUES(?)", h[:]); e != nil {
			return "", nil, e
		}
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM sessions"); e != nil {
		return "", nil, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM totp_steps"); e != nil {
		return "", nil, e
	}
	return secret, codes, tx.Commit()
}
func (a *Auth) RegenerateRecovery(ctx context.Context, password, otp string) ([]string, error) {
	var u string
	e := a.S.DB.QueryRowContext(ctx, "SELECT username FROM admin WHERE id=1").Scan(&u)
	if e != nil {
		return nil, e
	}
	_, _, e = a.Login(ctx, u, password, otp, time.Now())
	if e != nil {
		return nil, e
	}
	tx, e := a.S.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "DELETE FROM recovery"); e != nil {
		return nil, e
	}
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = strings.ToUpper(hex.EncodeToString(random(8)))
		h := sha256.Sum256([]byte(codes[i]))
		if _, e = tx.ExecContext(ctx, "INSERT INTO recovery(code_hash) VALUES(?)", h[:]); e != nil {
			return nil, e
		}
	}
	return codes, tx.Commit()
}
