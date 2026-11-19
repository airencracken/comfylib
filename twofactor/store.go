// SPDX-License-Identifier: AGPL-3.0-or-later

// Package twofactor provides optional authenticator enrollment and sign-in for
// applications that own their users and sessions in SQLite.
package twofactor

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/airencracken/comfylib/keyfile"
	"github.com/airencracken/comfylib/token"
	"github.com/airencracken/comfylib/totp"
)

// Install creates the factor state as part of an application's schema migration.
func Install(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS twofactor_secrets (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 ciphertext BLOB NOT NULL CHECK(length(ciphertext)=60),
 key_id TEXT NOT NULL CHECK(length(key_id)=64),
 last_step INTEGER CHECK(last_step>=0),
 draft_session TEXT NOT NULL CHECK(length(draft_session) IN (0,64)),
 draft_password TEXT NOT NULL CHECK(length(draft_password) IN (0,64)),
 draft_until INTEGER NOT NULL,
 challenge_attempts INTEGER NOT NULL DEFAULT 0 CHECK(challenge_attempts BETWEEN 0 AND 12),
 challenge_end INTEGER NOT NULL DEFAULT 0,
 CHECK((last_step IS NULL AND length(draft_session)=64 AND length(draft_password)=64 AND draft_until>0)
       OR (last_step IS NOT NULL AND draft_session='' AND draft_password='' AND draft_until=0))
);
CREATE TABLE IF NOT EXISTS twofactor_recovery (
 user_id INTEGER NOT NULL REFERENCES twofactor_secrets(user_id) ON DELETE CASCADE,
 digest TEXT NOT NULL CHECK(length(digest)=64),
 PRIMARY KEY(user_id,digest)
);
CREATE TABLE IF NOT EXISTS twofactor_challenges (
 digest TEXT PRIMARY KEY CHECK(length(digest)=64),
 user_id INTEGER NOT NULL REFERENCES twofactor_secrets(user_id) ON DELETE CASCADE,
 browser TEXT NOT NULL CHECK(length(browser)=64),
 password TEXT NOT NULL CHECK(length(password)=64),
 expires INTEGER NOT NULL,
 remaining INTEGER NOT NULL CHECK(remaining BETWEEN 0 AND 5),
 UNIQUE(user_id,browser)
);
CREATE INDEX IF NOT EXISTS twofactor_challenges_expiry ON twofactor_challenges(expires);
`)
	return err
}

// Sessions describes the application's existing account and session schema.
// CredentialSQL selects the password hash of an eligible user by ID.
type Sessions struct {
	CredentialSQL string
	TokenColumn   string
	ExpiryColumn  string
	Lifetime      time.Duration
	Milliseconds  bool
}

type store struct {
	db                               *sql.DB
	cipher                           cipher.AEAD
	keyID                            string
	sessions                         Sessions
	insert, expired, session, revoke string
}

const (
	draftTTL     = 10 * time.Minute
	challengeTTL = 5 * time.Minute
)

var (
	errInvalid  = errors.New("invalid authenticator or recovery code")
	errExpired  = errors.New("two-factor sign-in or setup has expired")
	errPassword = errors.New("password or session has changed")
	errRate     = errors.New("too many two-factor attempts")
	errKey      = errors.New("twofactor: restore the instance key that protects enrolled accounts")
)

func open(ctx context.Context, db *sql.DB, key string, sessions Sessions) (*store, error) {
	if (sessions.TokenColumn != "token_hash" && sessions.TokenColumn != "token") ||
		(sessions.ExpiryColumn != "expires_at" && sessions.ExpiryColumn != "expires") ||
		sessions.CredentialSQL == "" || sessions.Lifetime <= 0 {
		return nil, errors.New("twofactor: invalid session configuration")
	}
	s := &store{db: db, sessions: sessions}
	s.insert = fmt.Sprintf("INSERT INTO sessions(%s,user_id,%s) VALUES(?,?,?)", sessions.TokenColumn, sessions.ExpiryColumn)
	s.expired = "DELETE FROM sessions WHERE " + sessions.ExpiryColumn + "<=?"
	s.session = fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM sessions WHERE %s=? AND user_id=? AND %s>?)", sessions.TokenColumn, sessions.ExpiryColumn)
	s.revoke = "DELETE FROM sessions WHERE user_id=? AND " + sessions.TokenColumn + "<>?"
	if key != "" {
		raw, err := keyfile.Decode([]byte(key), 32, keyfile.Hex)
		if err != nil {
			return nil, fmt.Errorf("twofactor: invalid instance key: %w", err)
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return nil, err
		}
		s.cipher, err = cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, err
		}
		s.keyID = token.Hash(string(raw))
	}
	var mismatched bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM twofactor_secrets WHERE last_step IS NOT NULL AND key_id<>?)`, s.keyID).Scan(&mismatched)
	if err != nil {
		return nil, err
	}
	if mismatched {
		return nil, errKey
	}
	return s, nil
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *store) credential(ctx context.Context, q queryer, id int64) (string, error) {
	var hash string
	err := q.QueryRowContext(ctx, s.sessions.CredentialSQL, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errPassword
	}
	return hash, err
}

func (s *store) timestamp(now time.Time) int64 {
	if s.sessions.Milliseconds {
		return now.UnixMilli()
	}
	return now.Unix()
}

func (s *store) confirmSession(ctx context.Context, tx *sql.Tx, id int64, session string, now time.Time) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, s.session, token.Hash(session), id, s.timestamp(now)).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errPassword
	}
	return nil
}

func secretAAD(id int64) []byte {
	return fmt.Appendf(nil, "comfylib two-factor secret user %d", id)
}

func (s *store) decrypt(id int64, ciphertext []byte) (string, error) {
	secret, err := s.cipher.Open(nil, nil, ciphertext, secretAAD(id))
	if err != nil {
		return "", fmt.Errorf("twofactor: could not decrypt saved secret: %w", err)
	}
	return string(secret), nil
}

func (s *store) issueSession(ctx context.Context, tx *sql.Tx, id int64, now time.Time) (string, error) {
	raw, digest := token.New()
	if _, err := tx.ExecContext(ctx, s.expired, s.timestamp(now)); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, s.insert, digest, id, s.timestamp(now.Add(s.sessions.Lifetime))); err != nil {
		return "", err
	}
	return raw, nil
}

type login struct {
	session, challenge string
}

func (s *store) firstFactor(ctx context.Context, id int64, verifiedHash, browser string, now time.Time) (login, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return login{}, err
	}
	defer func() { _ = tx.Rollback() }()
	password, err := s.credential(ctx, tx, id)
	if err != nil {
		return login{}, err
	}
	if verifiedHash != "" && !token.Equal(verifiedHash, password) {
		return login{}, errPassword
	}
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM twofactor_secrets WHERE user_id=? AND last_step IS NOT NULL)`, id).Scan(&enabled); err != nil {
		return login{}, err
	}
	if !enabled {
		raw, err := s.issueSession(ctx, tx, id, now)
		if err != nil {
			return login{}, err
		}
		return login{session: raw}, tx.Commit()
	}
	if s.cipher == nil {
		return login{}, errKey
	}
	if browser == "" {
		return login{}, errExpired
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM twofactor_challenges WHERE expires<=?`, now.Unix()); err != nil {
		return login{}, err
	}
	raw, digest := token.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO twofactor_challenges(digest,user_id,browser,password,expires,remaining)
 VALUES(?,?,?,?,?,5) ON CONFLICT(user_id,browser) DO UPDATE SET digest=excluded.digest,
 password=excluded.password,expires=excluded.expires,remaining=excluded.remaining`,
		digest, id, token.Hash(browser), token.Hash(password), now.Add(challengeTTL).Unix())
	if err != nil {
		return login{}, err
	}
	return login{challenge: raw}, tx.Commit()
}

func (s *store) prompt(ctx context.Context, key, browser string, now time.Time) error {
	if len(key) != 64 || browser == "" {
		return errExpired
	}
	var found bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM twofactor_challenges WHERE digest=? AND browser=? AND remaining>0 AND expires>?)`,
		token.Hash(key), token.Hash(browser), now.Unix()).Scan(&found)
	if err == nil && !found {
		return errExpired
	}
	return err
}

func (s *store) secondFactor(ctx context.Context, key, browser, code string, now time.Time) (string, error) {
	if len(key) != 64 || browser == "" {
		return "", errExpired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	var stamp string
	digest := token.Hash(key)
	err = tx.QueryRowContext(ctx, `UPDATE twofactor_challenges SET remaining=remaining-1
 WHERE digest=? AND browser=? AND remaining>0 AND expires>? RETURNING user_id,password`,
		digest, token.Hash(browser), now.Unix()).Scan(&id, &stamp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errExpired
	}
	if err != nil {
		return "", err
	}
	password, err := s.credential(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if !token.Equal(token.Hash(password), stamp) {
		return "", errPassword
	}
	if err = takeAttempt(ctx, tx, id, now); err != nil {
		return "", err
	}
	accepted, err := s.consume(ctx, tx, id, code, now)
	if err != nil {
		return "", err
	}
	if !accepted {
		if err = tx.Commit(); err != nil {
			return "", err
		}
		return "", errInvalid
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM twofactor_challenges WHERE digest=?", digest); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE twofactor_secrets SET challenge_attempts=0,challenge_end=0 WHERE user_id=?", id); err != nil {
		return "", err
	}
	raw, err := s.issueSession(ctx, tx, id, now)
	if err != nil {
		return "", err
	}
	return raw, tx.Commit()
}

func takeAttempt(ctx context.Context, tx *sql.Tx, id int64, now time.Time) error {
	allowed, err := changed(tx.ExecContext(ctx, `UPDATE twofactor_secrets
 SET challenge_attempts=CASE WHEN challenge_end<=?1 THEN 1 ELSE challenge_attempts+1 END,
     challenge_end=CASE WHEN challenge_end<=?1 THEN ?2 ELSE challenge_end END
 WHERE user_id=?3 AND (challenge_end<=?1 OR challenge_attempts<12)`,
		now.Unix(), now.Add(15*time.Minute).Unix(), id))
	if err == nil && !allowed {
		return errRate
	}
	return err
}

func (s *store) consume(ctx context.Context, tx *sql.Tx, id int64, code string, now time.Time) (bool, error) {
	if len(code) > 100 {
		return false, nil
	}
	if !totp.IsCode(code) {
		result, err := tx.ExecContext(ctx, "DELETE FROM twofactor_recovery WHERE user_id=? AND digest=?", id, totp.HashRecoveryCode(code))
		return changed(result, err)
	}
	var ciphertext []byte
	err := tx.QueryRowContext(ctx, "SELECT ciphertext FROM twofactor_secrets WHERE user_id=? AND last_step IS NOT NULL", id).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	secret, err := s.decrypt(id, ciphertext)
	if err != nil {
		return false, err
	}
	step, ok := totp.Match(secret, code, now)
	if !ok {
		return false, nil
	}
	return changed(tx.ExecContext(ctx, "UPDATE twofactor_secrets SET last_step=? WHERE user_id=? AND last_step<?", step, id, step))
}

func changed(result sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
