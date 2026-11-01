package main

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/screenshot"
)

func withSagaImageURL(q contract.Quest) contract.Quest {
	if q.HasImage {
		q.ImageURL = "/v1/sagas/" + q.ID + "/image"
	}
	return q
}

func (s *Server) createSaga(w http.ResponseWriter, r *http.Request) {
	body, image, contentType, err := readQuestCreate(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	if rejectActorOverride(w, r, strings.TrimSpace(body.CreatedBy) != "") {
		return
	}
	in, err := fillQuestInput(body)
	if err != nil {
		s.fail(w, err)
		return
	}
	in.CreatedBy = ""
	p := playerFrom(r)
	out, err := s.store.CreateQuestForPlayer(r.Context(), p.ID, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(image) == 0 && strings.TrimSpace(body.ImageBase64) != "" {
		image, contentType, err = decodeCover(body.ImageBase64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
	}
	if len(image) > 0 {
		image, contentType, err = screenshot.NormalizeUpload(image)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		out, err = s.store.SetQuestImage(r.Context(), p.ID, out.ID, contentType, image)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	s.announceCreated(r.Context(), out, body.Announce)
	writeJSON(w, http.StatusCreated, s.withSignedCoverURL(withSagaImageURL(out)))
}

func (s *Server) listSagas(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	out, err := s.store.ListQuestsByPlayer(r.Context(), p.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	list := make([]contract.Quest, 0, len(out))
	for _, q := range out {
		list = append(list, s.withSignedCoverURL(withSagaImageURL(q)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sagas": list})
}

func (s *Server) getSaga(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	id := r.PathValue("id")
	if err := s.store.assertQuestReader(r.Context(), id, p.ID); err != nil {
		s.fail(w, err)
		return
	}
	mode := strings.TrimSpace(r.URL.Query().Get("sort"))
	if err := validateActivitySort(mode); err != nil {
		s.fail(w, err)
		return
	}
	out, err := s.store.GetQuest(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	sortCompleters(out.CompletedBy, mode)
	if mode != "" {
		out.Card = buildActivityCard(out.CompletedBy)
	}
	out.Quest = s.withSignedCoverURL(withSagaImageURL(out.Quest))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) patchSaga(w http.ResponseWriter, r *http.Request) {
	body, image, contentType, err := readQuestPatch(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	createdBy := body.CreatedBy != nil && strings.TrimSpace(*body.CreatedBy) != ""
	if body.UpdatedBy != nil && strings.TrimSpace(*body.UpdatedBy) != "" {
		createdBy = true
	}
	if body.EditorEmail != nil && strings.TrimSpace(*body.EditorEmail) != "" {
		createdBy = true
	}
	if rejectActorOverride(w, r, createdBy) {
		return
	}
	patch, err := fillQuestPatch(body)
	if err != nil {
		s.fail(w, err)
		return
	}
	p := playerFrom(r)
	id := r.PathValue("id")
	if !questPatchHasFields(patch) && len(image) > 0 {
		image, contentType, err = screenshot.NormalizeUpload(image)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		out, err := s.store.SetQuestImage(r.Context(), p.ID, id, contentType, image)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.withSignedCoverURL(withSagaImageURL(out)))
		return
	}
	detail, gerr := s.store.GetQuest(r.Context(), id)
	out, err := s.store.UpdateQuestForPlayer(r.Context(), p.ID, id, patch)
	if err != nil {
		s.fail(w, err)
		return
	}
	if gerr == nil {
		actor := displayActor(p.DisplayName, "", p.BattleTag)
		s.announceEdited(r.Context(), detail.Quest, out, body.Announce, actor, patch.Changes)
		_ = image
		_ = contentType
	}
	writeJSON(w, http.StatusOK, s.withSignedCoverURL(withSagaImageURL(out)))
}

func (s *Server) putSagaImage(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	id := r.PathValue("id")
	image, contentType, err := readSagaImage(w, r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	image, contentType, err = screenshot.NormalizeUpload(image)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	out, err := s.store.SetQuestImage(r.Context(), p.ID, id, contentType, image)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withSignedCoverURL(withSagaImageURL(out)))
}

func (s *Server) deleteSagaImage(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	out, err := s.store.ClearQuestImage(r.Context(), p.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withSignedCoverURL(withSagaImageURL(out)))
}

func (s *Server) getSagaImage(w http.ResponseWriter, r *http.Request) {
	// Any signed-in player may fetch a cover used in sync / the addon.
	_ = playerFrom(r)
	s.writeStoredQuestImage(w, r)
}

func (s *Server) getAdminQuestImage(w http.ResponseWriter, r *http.Request) {
	s.writeStoredQuestImage(w, r)
}

func (s *Server) writeStoredQuestImage(w http.ResponseWriter, r *http.Request) {
	ct, image, err := s.store.QuestImage(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
}

func readSagaImage(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, 12<<20)
		if err := r.ParseMultipartForm(12 << 20); err != nil {
			return nil, "", err
		}
		file, hdr, err := r.FormFile("image")
		if err != nil {
			return nil, "", errors.New("multipart field \"image\" is required")
		}
		defer file.Close()
		b, err := io.ReadAll(io.LimitReader(file, 8<<20+1))
		if err != nil {
			return nil, "", err
		}
		if len(b) > 8<<20 {
			return nil, "", errors.New("image larger than 8 MiB")
		}
		contentType := hdr.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		return b, contentType, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20+1)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", err
	}
	if len(b) == 0 {
		return nil, "", errors.New("empty body")
	}
	if len(b) > 8<<20 {
		return nil, "", errors.New("image larger than 8 MiB")
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	return b, ct, nil
}
