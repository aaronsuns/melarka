package api

import (
	"errors"
	"net/http"

	"github.com/aaronsuns/lark-server/internal/usageping"
)

// GET/PUT /admin/usage-ping: the anonymous install count (internal/usageping).
// "env_off" means LARK_USAGE_PING=off, which the switch cannot override.
func (s *Server) usagePingGet(w http.ResponseWriter, r *http.Request) {
	on, err := s.UsagePing.Enabled(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": on, "env_off": s.UsagePing.Off})
}

func (s *Server) usagePingPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Enabled == nil {
		writeError(w, 400, "enabled (true or false) is required")
		return
	}
	if err := s.UsagePing.SetEnabled(r.Context(), *body.Enabled); errors.Is(err, usageping.ErrOff) {
		writeCoded(w, 409, "usage_ping_env_off", "the usage count is off by LARK_USAGE_PING")
		return
	} else if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": *body.Enabled, "env_off": false})
}
