package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     Role   `json:"role"`
}

type Device struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
}

var (
	ErrBadCredentials = errors.New("invalid credentials")
	ErrNotFound       = errors.New("not found")
	ErrExists         = errors.New("already exists")
	ErrInvalid        = errors.New("username, password and a valid role are required")
)

// dummyHash is a fixed, valid argon2id hash computed once at startup so
// Login can run a real VerifyPassword against it on the "no such user" path.
// This keeps that path's cost equal to the "wrong password" path, closing a
// timing side channel that would otherwise let an attacker enumerate valid
// usernames.
var dummyHash = func() string {
	h, err := HashPassword("lark-timing-defense-dummy-password")
	if err != nil {
		panic(err)
	}
	return h
}()

type Store struct {
	DB  *sql.DB
	Now func() time.Time

	// failMu guards fails, the per-username consecutive-failure tracker
	// backing Login's throttle (see throttle.go).
	failMu sync.Mutex
	fails  map[string]loginFails
}

func (s *Store) now() int64 {
	if s.Now != nil {
		return s.Now().Unix()
	}
	return time.Now().Unix()
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CreateUser(ctx context.Context, username, password string, role Role) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" || (role != RoleAdmin && role != RoleMember) {
		return User{}, ErrInvalid
	}
	h, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO users(username,password_hash,role,created_at) VALUES (?,?,?,?)`, username, h, role, s.now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrExists
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, Role: role}, nil
}

func (s *Store) EnsureAdmin(ctx context.Context, username, password string) error {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if username == "" || password == "" {
		return errors.New("no admin exists: set LARK_ADMIN_USER and LARK_ADMIN_PASSWORD for the first start")
	}
	_, err := s.CreateUser(ctx, username, password, RoleAdmin)
	return err
}

func (s *Store) Login(ctx context.Context, username, password, deviceName string) (string, User, error) {
	key := strings.ToLower(strings.TrimSpace(username))
	// Per-username backoff comes before the semaphore and before any argon2
	// at all: once a username has racked up enough recent failures, further
	// attempts for it must cost nothing until the window passes, not just
	// wait their turn for a verification slot.
	if s.throttled(key) {
		return "", User{}, ErrThrottled
	}
	if !acquireVerifySlot() {
		return "", User{}, ErrBusy
	}
	defer releaseVerifySlot()

	var u User
	var h string
	err := s.DB.QueryRowContext(ctx, `SELECT id,username,role,password_hash FROM users WHERE username=?`, strings.TrimSpace(username)).
		Scan(&u.ID, &u.Username, &u.Role, &h)
	if errors.Is(err, sql.ErrNoRows) {
		// No such user: still hash the supplied password against a fixed
		// dummy hash so this path costs the same as a wrong-password path,
		// and an attacker can't tell "no such user" from "wrong password"
		// by timing.
		verify(dummyHash, password)
		s.recordFailure(key)
		return "", User{}, ErrBadCredentials
	}
	if err != nil {
		return "", User{}, err
	}
	if !verify(h, password) {
		s.recordFailure(key)
		return "", User{}, ErrBadCredentials
	}
	s.recordSuccess(key)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", User{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if deviceName == "" {
		deviceName = "unknown device"
	}
	now := s.now()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO devices(user_id,token_hash,name,created_at,last_seen_at) VALUES (?,?,?,?,?)`,
		u.ID, hashToken(tok), deviceName, now, now); err != nil {
		return "", User{}, err
	}
	return tok, u, nil
}

// CheckPassword verifies password against userID's current hash without
// creating a device row or a token, so callers who only need to confirm the
// caller's current password (e.g. before changing it) don't leave a
// throwaway device behind. It shares Login's semaphore: argon2id's cost is
// the same regardless of which code path runs it, so an unbounded number of
// concurrent CheckPassword calls could starve logins exactly as unbounded
// logins could.
func (s *Store) CheckPassword(ctx context.Context, userID int64, password string) (bool, error) {
	var h string
	err := s.DB.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=?`, userID).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !acquireVerifySlot() {
		return false, ErrBusy
	}
	defer releaseVerifySlot()
	return verify(h, password), nil
}

func (s *Store) Authenticate(ctx context.Context, tok string) (User, int64, error) {
	if tok == "" {
		return User{}, 0, ErrBadCredentials
	}
	var u User
	var dev, lastSeen int64
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.username,u.role,d.id,d.last_seen_at FROM devices d JOIN users u ON u.id=d.user_id WHERE d.token_hash=?`,
		hashToken(tok)).Scan(&u.ID, &u.Username, &u.Role, &dev, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, 0, ErrBadCredentials
	}
	if err != nil {
		return User{}, 0, err
	}
	// Write last_seen at most every 10 minutes: every stream request authenticates.
	if now := s.now(); now-lastSeen > 600 {
		s.DB.ExecContext(ctx, `UPDATE devices SET last_seen_at=? WHERE id=?`, now, dev)
	}
	return u, dev, nil
}

func (s *Store) Logout(ctx context.Context, deviceID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE id=?`, deviceID)
	return err
}

func (s *Store) Devices(ctx context.Context, userID int64) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,created_at,last_seen_at FROM devices WHERE user_id=? ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) RevokeDevice(ctx context.Context, userID, deviceID int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE id=? AND user_id=?`, deviceID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetPassword(ctx context.Context, userID int64, password string) error {
	if password == "" {
		return errors.New("password required")
	}
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, h, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,username,role FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
