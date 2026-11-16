package auth

import (
	"context"
	"errors"
	"testing"
)

// signIn creates a user (if new) and signs it in, returning its token and device id.
func signIn(t *testing.T, s *Store, name string) (string, int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, name, "pw-"+name+"-long", RoleMember); err != nil && !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	tok, _, err := s.Login(ctx, name, "pw-"+name+"-long", "phone")
	if err != nil {
		t.Fatal(err)
	}
	_, dev, err := s.Authenticate(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	return tok, dev
}

func names(accts []LinkedAccount) map[string]bool {
	m := map[string]bool{}
	for _, a := range accts {
		m[a.Username] = a.Current
	}
	return m
}

func TestLinkSwitchAndBack(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, dad := signIn(t, s, "dad")
	if a, _ := s.LinkedAccounts(ctx, dad); len(a) != 1 || !a[0].Current || a[0].Username != "dad" {
		t.Fatalf("alone: %+v", a)
	}
	_, kid := signIn(t, s, "kid")
	if err := s.LinkDevice(ctx, dad, kid); err != nil {
		t.Fatal(err)
	}
	if got := names(must(s.LinkedAccounts(ctx, kid))); len(got) != 2 || !got["kid"] || got["dad"] {
		t.Fatalf("from kid: %v", got)
	}
	// Switch back to dad from the kid's device: a fresh token for dad's device.
	var dadID int64
	s.DB.QueryRow(`SELECT id FROM users WHERE username='dad'`).Scan(&dadID)
	tok, u, err := s.SwitchAccount(ctx, kid, dadID)
	if err != nil || u.Username != "dad" {
		t.Fatalf("switch: %v %+v", err, u)
	}
	if got, dev, err := s.Authenticate(ctx, tok); err != nil || got.Username != "dad" || dev != dad {
		t.Fatalf("new token: %v %+v %d", err, got, dev)
	}
	// The kid's device keeps working (a request in flight must not 401).
	var kidID int64
	s.DB.QueryRow(`SELECT id FROM users WHERE username='kid'`).Scan(&kidID)
	tok2, _, err := s.SwitchAccount(ctx, dad, kidID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Authenticate(ctx, tok); err != nil {
		t.Fatal("dad's token died when switching away from it")
	}
	if u, _, _ := s.Authenticate(ctx, tok2); u.Username != "kid" {
		t.Fatal("kid token")
	}
}

func TestSwitchOnlyWithinTheGroup(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, dad := signIn(t, s, "dad")
	_, other := signIn(t, s, "stranger")
	var strangerID int64
	s.DB.QueryRow(`SELECT id FROM users WHERE username='stranger'`).Scan(&strangerID)
	if _, _, err := s.SwitchAccount(ctx, dad, strangerID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("switched outside the group: %v", err)
	}
	_ = other
}

func TestRelinkingReplacesTheOlderDeviceOfThatUser(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, dad := signIn(t, s, "dad")
	oldTok, kid1 := signIn(t, s, "kid")
	s.LinkDevice(ctx, dad, kid1)
	_, kid2 := signIn(t, s, "kid")
	if err := s.LinkDevice(ctx, dad, kid2); err != nil {
		t.Fatal(err)
	}
	if a := must(s.LinkedAccounts(ctx, dad)); len(a) != 2 {
		t.Fatalf("accounts %+v", a)
	}
	if _, _, err := s.Authenticate(ctx, oldTok); err == nil {
		t.Fatal("the replaced device still signs in")
	}
}

func TestUnlinkAndLogoutAll(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_, dad := signIn(t, s, "dad")
	_, kid := signIn(t, s, "kid")
	_, mum := signIn(t, s, "mum")
	s.LinkDevice(ctx, dad, kid)
	s.LinkDevice(ctx, kid, mum)
	var kidID, dadID int64
	s.DB.QueryRow(`SELECT id FROM users WHERE username='kid'`).Scan(&kidID)
	s.DB.QueryRow(`SELECT id FROM users WHERE username='dad'`).Scan(&dadID)
	if err := s.UnlinkAccount(ctx, dad, dadID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("removed the current account: %v", err)
	}
	if err := s.UnlinkAccount(ctx, dad, kidID); err != nil {
		t.Fatal(err)
	}
	if got := names(must(s.LinkedAccounts(ctx, dad))); len(got) != 2 || got["kid"] {
		t.Fatalf("after unlink: %v", got)
	}
	if err := s.LogoutLinked(ctx, dad); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d devices left after signing out all", n)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
