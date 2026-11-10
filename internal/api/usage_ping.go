package api

import "net/http"

// GET/PUT /admin/usage-ping: the opt-in anonymous install count (internal/usageping).
func (s *Server) usagePingGet(w http.ResponseWriter, r *http.Request) {
	on, err := s.UsagePing.Enabled(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": on})
}

func (s *Server) usagePingPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Enabled == nil {
		writeError(w, 400, "enabled (true or false) is required")
		return
	}
	if err := s.UsagePing.SetEnabled(r.Context(), *body.Enabled); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": *body.Enabled})
}
