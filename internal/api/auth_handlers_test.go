package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestLoginMeLogout(t *testing.T) {
	s, ts := newTestServer(t)
	loginAs(t, s, "alice", "admin")

	resp, body := do(t, ts, "", "POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "bad", "device_name": "x"})
	if resp.StatusCode != 401 {
		t.Fatalf("bad pw code=%d", resp.StatusCode)
	}

	resp, body = do(t, ts, "", "POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "pw", "device_name": "iPhone"})
	if resp.StatusCode != 200 {
		t.Fatalf("login code=%d body=%s", resp.StatusCode, body)
	}
	var lr struct {
		Token string
		User  struct{ Username, Role string }
	}
	json.Unmarshal(body, &lr)
	if lr.Token == "" || lr.User.Role != "admin" {
		t.Fatalf("login body=%s", body)
	}
	if !strings.Contains(resp.Header.Get("Set-Cookie"), "lark_token=") {
		t.Fatal("no cookie")
	}

	resp, body = do(t, ts, lr.Token, "GET", "/api/v1/me", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"alice"`) {
		t.Fatalf("me %d %s", resp.StatusCode, body)
	}

	resp, body = do(t, ts, lr.Token, "GET", "/api/v1/devices", nil)
	if !strings.Contains(string(body), "iPhone") {
		t.Fatalf("devices %s", body)
	}

	do(t, ts, lr.Token, "POST", "/api/v1/auth/logout", nil)
	resp, _ = do(t, ts, lr.Token, "GET", "/api/v1/me", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("after logout code=%d", resp.StatusCode)
	}
}

func TestUnauthenticatedAPIIs401AndUIIsServed(t *testing.T) {
	_, ts := newTestServer(t)
	resp, _ := do(t, ts, "", "GET", "/api/v1/me", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("code=%d", resp.StatusCode)
	}
	resp, body := do(t, ts, "", "GET", "/some/spa/route", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Melarka") {
		t.Fatalf("ui code=%d", resp.StatusCode)
	}
	resp, _ = do(t, ts, "", "GET", "/api/v1/nope", nil)
	if resp.StatusCode != 401 && resp.StatusCode != 404 {
		t.Fatalf("unknown api must not return the SPA, got %d", resp.StatusCode)
	}
}

func TestRevokeDevice(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "alice", "admin")

	resp, body := do(t, ts, tok, "GET", "/api/v1/devices", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("devices code=%d body=%s", resp.StatusCode, body)
	}
	var devices []struct{ ID int64 }
	if err := json.Unmarshal(body, &devices); err != nil || len(devices) != 1 {
		t.Fatalf("devices body=%s err=%v", body, err)
	}
	ownID := devices[0].ID

	resp, body = do(t, ts, tok, "DELETE", "/api/v1/devices/999999", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("unknown id code=%d body=%s", resp.StatusCode, body)
	}

	resp, body = do(t, ts, tok, "DELETE", "/api/v1/devices/"+strconv.FormatInt(ownID, 10), nil)
	if resp.StatusCode != 204 {
		t.Fatalf("revoke own device code=%d body=%s", resp.StatusCode, body)
	}

	resp, _ = do(t, ts, tok, "GET", "/api/v1/me", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("token should be revoked, code=%d", resp.StatusCode)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	s, ts := newTestServer(t)
	tok := loginAs(t, s, "kid", "member")
	resp, _ := do(t, ts, tok, "PUT", "/api/v1/me/password", map[string]string{"current": "wrong", "new": "x"})
	if resp.StatusCode != 403 {
		t.Fatalf("wrong current code=%d", resp.StatusCode)
	}
	resp, _ = do(t, ts, tok, "PUT", "/api/v1/me/password", map[string]string{"current": "pw", "new": "x2"})
	if resp.StatusCode != 204 {
		t.Fatalf("code=%d", resp.StatusCode)
	}
}
