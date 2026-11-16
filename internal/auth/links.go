package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// Linked devices: the quick account switch (docs/superpowers/specs/quick-account-switch-design.md).
// Devices sharing devices.link_group were signed in on the same browser or app and may switch to each
// other without a password. A group holds at most one device per user.

type LinkedAccount struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Current  bool   `json:"current"`
}

func (s *Store) group(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, dev int64) (string, error) {
	var g sql.NullString
	err := q.QueryRowContext(ctx, `SELECT link_group FROM devices WHERE id=?`, dev).Scan(&g)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return g.String, err
}

// LinkDevice puts newDev into fromDev's group, making the group if fromDev has none. Any other device of
// newDev's user in that group is deleted.
func (s *Store) LinkDevice(ctx context.Context, fromDev, newDev int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	g, err := s.group(ctx, tx, fromDev)
	if err != nil {
		return err
	}
	if g == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		g = hex.EncodeToString(b)
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET link_group=? WHERE id=?`, g, fromDev); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE link_group=? AND id<>? AND user_id=(SELECT user_id FROM devices WHERE id=?)`,
		g, newDev, newDev); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET link_group=? WHERE id=?`, g, newDev); err != nil {
		return err
	}
	return tx.Commit()
}

// LinkedAccounts lists the accounts signed in on dev's browser or app, dev's own marked current.
func (s *Store) LinkedAccounts(ctx context.Context, dev int64) ([]LinkedAccount, error) {
	g, err := s.group(ctx, s.DB, dev)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id, u.username, d.id = ? FROM devices d JOIN users u ON u.id = d.user_id
		WHERE d.id = ? OR (? <> '' AND d.link_group = ?) ORDER BY u.username COLLATE NOCASE`, dev, dev, g, g)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LinkedAccount{}
	for rows.Next() {
		var a LinkedAccount
		if err := rows.Scan(&a.UserID, &a.Username, &a.Current); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SwitchAccount gives userID's device in dev's group a new token and returns it. Tokens are stored only as
// hashes, so the old one cannot be handed out again; dev itself keeps its token.
func (s *Store) SwitchAccount(ctx context.Context, dev, userID int64) (string, User, error) {
	g, err := s.group(ctx, s.DB, dev)
	if err != nil {
		return "", User{}, err
	}
	if g == "" {
		return "", User{}, ErrNotFound
	}
	var target int64
	var u User
	err = s.DB.QueryRowContext(ctx, `SELECT d.id, u.id, u.username, u.role FROM devices d JOIN users u ON u.id = d.user_id
		WHERE d.link_group = ? AND d.user_id = ?`, g, userID).Scan(&target, &u.ID, &u.Username, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", User{}, ErrNotFound
	}
	if err != nil {
		return "", User{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", User{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := s.DB.ExecContext(ctx, `UPDATE devices SET token_hash=?, last_seen_at=? WHERE id=?`, hashToken(tok), s.now(), target); err != nil {
		return "", User{}, err
	}
	return tok, u, nil
}

// UnlinkAccount signs userID out of dev's group. The current account cannot be removed this way (ErrInvalid).
func (s *Store) UnlinkAccount(ctx context.Context, dev, userID int64) error {
	var cur int64
	if err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM devices WHERE id=?`, dev).Scan(&cur); err != nil {
		return err
	}
	if cur == userID {
		return ErrInvalid
	}
	g, err := s.group(ctx, s.DB, dev)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE ? <> '' AND link_group=? AND user_id=?`, g, g, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LogoutLinked deletes dev and every device in its group: signing out signs out every account on it.
func (s *Store) LogoutLinked(ctx context.Context, dev int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE id=? OR link_group=(SELECT link_group FROM devices WHERE id=?)`, dev, dev)
	return err
}
