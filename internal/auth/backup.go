package auth

import (
	"context"
	"database/sql"
	"encoding/base32"
	"errors"
	"strings"

	"herdr-space/internal/store"
)

// VerifyBackupKey confirms that this database can decrypt its configured
// authenticator secret with the accompanying key, without exposing it.
func (a *Auth) VerifyBackupKey(ctx context.Context) error {
	var nonce, ciphertext []byte
	err := a.S.DB.QueryRowContext(ctx, "SELECT totp_nonce,totp_cipher FROM admin WHERE id=1").Scan(&nonce, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	secret, err := a.decrypt(nonce, ciphertext)
	if err != nil {
		return store.ErrInvalid
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil || len(decoded) != 20 || len(secret) != 32 {
		return store.ErrInvalid
	}
	return nil
}
