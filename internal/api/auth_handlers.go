package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aaronsuns/lark-server/internal/auth"
)

func (s *Server) authRoutes(a chi.Router) {
	a.Post("/auth/logout", s.logout)
	a.Get("/me", s.me)
	a.Put("/me/password", s.changePassword)
	a.Get("/devices", s.devices)
	a.Delete("/devices/{id}", s.revokeDevice)
	a.Get("/me/preferences", s.getPrefs)
	a.Put("/me/preferences", s.putPrefs)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		DeviceName string `json:"device_name"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	tok, u, err := s.Auth.Login(r.Context(), in.Username, in.Password, in.DeviceName)
	if errors.Is(err, auth.ErrBadCredentials) {
		writeCoded(w, 401, "wrong_credentials", "wrong username or password")
		return
	}
	if errors.Is(err, auth.ErrBusy) || errors.Is(err, auth.ErrThrottled) {
		writeCoded(w, 429, "too_many_attempts", "too many login attempts, try again shortly")
		return
	}
	if err != nil {
		s.Log.Error("login", "err", err)
		writeError(w, 500, "login failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: tok, Path: "/", HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode, MaxAge: 10 * 365 * 24 * 3600,
	})
	writeJSON(w, 200, map[string]any{"token": tok, "user": u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(r.Context(), auth.DeviceIDFrom(r.Context()))
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(204)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, auth.UserFrom(r.Context()))
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Current, New string }
	if err := decodeJSON(r, &in); err != nil || in.New == "" {
		writeError(w, 400, "current and new password required")
		return
	}
	u := auth.UserFrom(r.Context())
	ok, err := s.Auth.CheckPassword(r.Context(), u.ID, in.Current)
	if errors.Is(err, auth.ErrBusy) {
		writeCoded(w, 429, "too_many_attempts", "too many login attempts, try again shortly")
		return
	}
	if err != nil {
		s.Log.Error("changePassword", "err", err)
		writeError(w, 500, "password check failed")
		return
	}
	if !ok {
		writeCoded(w, 403, "current_password_wrong", "current password is wrong")
		return
	}
	if err := s.Auth.SetPassword(r.Context(), u.ID, in.New); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.Auth.Devices(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ds)
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r, "id")
	if !ok {
		writeError(w, 400, "bad id")
		return
	}
	if err := s.Auth.RevokeDevice(r.Context(), auth.UserFrom(r.Context()).ID, id); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, 404, "device not found")
			return
		}
		s.Log.Error("revokeDevice", "err", err)
		writeError(w, 500, "revoke device failed")
		return
	}
	w.WriteHeader(204)
}
