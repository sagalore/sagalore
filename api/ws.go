package main

import "net/http"

// serveWS is GET /v1/ws. The upgrade uses the same bearer as /v1/sync.
// The socket only carries a small event. The companion then calls /v1/sync.
func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	if s.events == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "realtime is off")
		return
	}
	s.withPlayer(func(w http.ResponseWriter, r *http.Request) {
		s.events.Serve(w, r, playerFrom(r).ID)
	})(w, r)
}
