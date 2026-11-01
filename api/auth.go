package main

import (
	"net/http"

	"github.com/sagalore/sagalore/internal/contract"
)

func (s *Server) upsertCharacter(w http.ResponseWriter, r *http.Request) {
	var in contract.CharacterUpsert
	if err := readJSON(r, &in, 1<<20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	out, err := s.store.UpsertCharacter(r.Context(), playerFrom(r).ID, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
