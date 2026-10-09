package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const CookieName = "lark_token"

type ctxKey int

const (
	userKey ctxKey = iota
	deviceKey
)

func tokenFrom(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	return ""
}

// writeErr writes {"error": msg, "code": code} — see api.writeCoded, which
// this mirrors; auth can't import package api (api already imports auth).
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}

func (s *Store) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, dev, err := s.Authenticate(r.Context(), tokenFrom(r))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "not_signed_in", "not signed in")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, deviceKey, dev)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()).Role != RoleAdmin {
			writeErr(w, http.StatusForbidden, "admin_only", "admin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func UserFrom(ctx context.Context) User {
	u, _ := ctx.Value(userKey).(User)
	return u
}

func DeviceIDFrom(ctx context.Context) int64 {
	d, _ := ctx.Value(deviceKey).(int64)
	return d
}
