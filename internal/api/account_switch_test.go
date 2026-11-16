package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aaronsuns/lark-server/internal/auth"
)

func TestQuickAccountSwitch(t *testing.T) {
	s, ts := newTestServer(t)
	dad := loginAs(t, s, "dad", auth.RoleAdmin)
	loginAs(t, s, "kid", auth.RoleMember)

	// Add account: signs in as kid on dad's device and switches to it.
	r, b := do(t, ts, dad, "POST", "/api/v1/auth/login", map[string]any{"username": "kid", "password": "pw", "link": true})
	if r.StatusCode != 200 {
		t.Fatalf("add: %d %s", r.StatusCode, b)
	}
	var in struct {
		Token string
		User  auth.User
	}
	json.Unmarshal(b, &in)
	if in.User.Username != "kid" || !hasCookie(r, in.Token) {
		t.Fatalf("add response %s", b)
	}
	kid := in.Token

	r, b = do(t, ts, kid, "GET", "/api/v1/auth/accounts", nil)
	var accts []auth.LinkedAccount
	if r.StatusCode != 200 || json.Unmarshal(b, &accts) != nil || len(accts) != 2 {
		t.Fatalf("accounts: %d %s", r.StatusCode, b)
	}
	var dadID int64
	for _, a := range accts {
		if a.Username == "dad" {
			dadID = a.UserID
			if a.Current {
				t.Fatal("dad marked current")
			}
		}
	}

	// Switch back to dad, no password.
	r, b = do(t, ts, kid, "POST", "/api/v1/auth/switch", map[string]any{"user_id": dadID})
	json.Unmarshal(b, &in)
	if r.StatusCode != 200 || in.User.Username != "dad" || !hasCookie(r, in.Token) {
		t.Fatalf("switch: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, in.Token, "GET", "/api/v1/me", nil); r.StatusCode != 200 || !json.Valid(b) {
		t.Fatalf("me after switch: %d", r.StatusCode)
	}

	// Adding the current account again leaves nothing behind.
	if r, b := do(t, ts, in.Token, "POST", "/api/v1/auth/login", map[string]any{"username": "dad", "password": "pw", "link": true}); r.StatusCode != 409 {
		t.Fatalf("re-add self: %d %s", r.StatusCode, b)
	}
	// A user who never signed in here cannot be switched to.
	stranger := loginAs(t, s, "stranger", auth.RoleMember)
	_ = stranger
	r, _ = do(t, ts, in.Token, "POST", "/api/v1/auth/switch", map[string]any{"user_id": 999})
	if r.StatusCode != 404 {
		t.Fatalf("switch to unlinked: %d", r.StatusCode)
	}
	// Linking needs a session.
	if r, _ := do(t, ts, "", "POST", "/api/v1/auth/login", map[string]any{"username": "kid", "password": "pw", "link": true}); r.StatusCode != 401 {
		t.Fatalf("link without session: %d", r.StatusCode)
	}

	// Sign out signs out every account here.
	if r, _ := do(t, ts, in.Token, "POST", "/api/v1/auth/logout", nil); r.StatusCode != 204 {
		t.Fatal("logout")
	}
	if r, _ := do(t, ts, kid, "GET", "/api/v1/me", nil); r.StatusCode != 401 {
		t.Fatalf("kid still signed in after sign out: %d", r.StatusCode)
	}
}

func hasCookie(r *http.Response, tok string) bool {
	for _, c := range r.Cookies() {
		if c.Name == auth.CookieName && c.Value == tok {
			return true
		}
	}
	return false
}
