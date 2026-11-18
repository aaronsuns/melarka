package api

import "net/http"

// GET /admin/update-check: the cached answer of the daily newer-release check
// (internal/updatecheck). Admins only; it never makes a request itself.
func (s *Server) updateCheckGet(w http.ResponseWriter, r *http.Request) {
	if s.UpdateCheck == nil {
		writeJSON(w, 200, map[string]any{"enabled": false, "current": "", "latest": "", "url": "", "newer": false, "checked_at": 0})
		return
	}
	writeJSON(w, 200, s.UpdateCheck.Status())
}
