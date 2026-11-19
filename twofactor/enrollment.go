// SPDX-License-Identifier: AGPL-3.0-or-later

package twofactor

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/airencracken/comfylib/token"
	"github.com/airencracken/comfylib/totp"
)

type account struct {
	enabled bool
	secret  string
	codes   int
}

func (s *store) account(ctx context.Context, id int64, session string, now time.Time) (account, error) {
	var v account
	var draft []byte
	err := s.db.QueryRowContext(ctx, `SELECT last_step IS NOT NULL,
 CASE WHEN draft_session=? AND draft_until>? AND key_id=? THEN ciphertext ELSE NULL END,
 (SELECT count(*) FROM twofactor_recovery WHERE user_id=?)
 FROM twofactor_secrets WHERE user_id=?`, token.Hash(session), now.Unix(), s.keyID, id, id).
		Scan(&v.enabled, &draft, &v.codes)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil || len(draft) == 0 {
		return v, err
	}
	v.secret, err = s.decrypt(id, draft)
	return v, err
}

func (s *store) requireCurrent(ctx context.Context, tx *sql.Tx, id int64, session, verifiedHash string, now time.Time) (string, error) {
	if err := s.confirmSession(ctx, tx, id, session, now); err != nil {
		return "", err
	}
	hash, err := s.credential(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if verifiedHash != "" && !token.Equal(hash, verifiedHash) {
		return "", errPassword
	}
	return hash, nil
}

func (s *store) begin(ctx context.Context, id int64, session, verifiedHash string, now time.Time) error {
	secret, err := totp.GenerateSecret()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	password, err := s.requireCurrent(ctx, tx, id, session, verifiedHash, now)
	if err != nil {
		return err
	}
	ciphertext := s.cipher.Seal(nil, nil, []byte(secret), secretAAD(id)) // #nosec G407 -- NewGCMWithRandomNonce generates and prepends its nonce.
	ok, err := changed(tx.ExecContext(ctx, `INSERT INTO twofactor_secrets(user_id,ciphertext,key_id,draft_session,draft_password,draft_until)
 VALUES(?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET ciphertext=excluded.ciphertext,key_id=excluded.key_id,
 draft_session=excluded.draft_session,draft_password=excluded.draft_password,draft_until=excluded.draft_until
 WHERE last_step IS NULL`, id, ciphertext, s.keyID, token.Hash(session), token.Hash(password), now.Add(draftTTL).Unix()))
	if err != nil {
		return err
	}
	if !ok {
		return errExpired
	}
	return tx.Commit()
}

func (s *store) enable(ctx context.Context, id int64, session, code string, now time.Time) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	password, err := s.requireCurrent(ctx, tx, id, session, "", now)
	if err != nil {
		return nil, err
	}
	var encrypted []byte
	err = tx.QueryRowContext(ctx, `SELECT ciphertext FROM twofactor_secrets WHERE user_id=? AND last_step IS NULL
 AND draft_session=? AND draft_password=? AND draft_until>? AND key_id=?`,
		id, token.Hash(session), token.Hash(password), now.Unix(), s.keyID).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errExpired
	}
	if err != nil {
		return nil, err
	}
	secret, err := s.decrypt(id, encrypted)
	if err != nil {
		return nil, err
	}
	step, ok := totp.Match(secret, code, now)
	if !ok {
		return nil, errInvalid
	}
	if _, err = tx.ExecContext(ctx, `UPDATE twofactor_secrets SET last_step=?,draft_session='',draft_password='',draft_until=0
 WHERE user_id=?`, step, id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, s.revoke, id, token.Hash(session)); err != nil {
		return nil, err
	}
	codes, err := replaceCodes(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return codes, tx.Commit()
}

func (s *store) change(ctx context.Context, id int64, session, verifiedHash, code string, remove bool, now time.Time) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = s.requireCurrent(ctx, tx, id, session, verifiedHash, now); err != nil {
		return nil, err
	}
	accepted, err := s.consume(ctx, tx, id, code, now)
	if err != nil {
		return nil, err
	}
	if !accepted {
		return nil, errInvalid
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM twofactor_challenges WHERE user_id=?", id); err != nil {
		return nil, err
	}
	if remove {
		if _, err = tx.ExecContext(ctx, "DELETE FROM twofactor_secrets WHERE user_id=?", id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, s.revoke, id, token.Hash(session)); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	codes, err := replaceCodes(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return codes, tx.Commit()
}

func replaceCodes(ctx context.Context, tx *sql.Tx, id int64) ([]string, error) {
	if _, err := tx.ExecContext(ctx, "DELETE FROM twofactor_recovery WHERE user_id=?", id); err != nil {
		return nil, err
	}
	codes := make([]string, 0, totp.RecoveryCodeCount)
	for range totp.RecoveryCodeCount {
		code, err := totp.GenerateRecoveryCode()
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO twofactor_recovery(user_id,digest) VALUES(?,?)", id, totp.HashRecoveryCode(code)); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}
