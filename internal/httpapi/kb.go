package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/aicode/kb-server/internal/kb"
	"github.com/go-chi/chi/v5"
)

type KBServer struct {
	kb *kb.Service
}

func NewKB(svc *kb.Service) *KBServer {
	return &KBServer{kb: svc}
}

func (s *KBServer) Routes(r chi.Router) {
	r.Route("/kb", func(r chi.Router) {
		r.Post("/collections", apiHandler(s.createCollection).ServeHTTP)
		r.Get("/collections", apiHandler(s.listCollections).ServeHTTP)
		r.Delete("/collections/{id}", apiHandler(s.deleteCollection).ServeHTTP)

		r.Post("/collections/{id}/documents", apiHandler(s.uploadDocument).ServeHTTP)
		r.Get("/documents/{id}", apiHandler(s.getDocument).ServeHTTP)
		r.Delete("/documents/{id}", apiHandler(s.deleteDocument).ServeHTTP)
		r.Post("/documents/{id}/retry", apiHandler(s.retryDocument).ServeHTTP)

		r.Post("/search", apiHandler(s.search).ServeHTTP)
		r.Post("/chat", apiHandler(s.chat).ServeHTTP)
	})
}

func (s *KBServer) createCollection(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return kb.NewValidationError("invalid request body: " + err.Error())
	}
	col, err := s.kb.CreateCollection(r.Context(), req.Name, req.Description)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, col)
	return nil
}

func (s *KBServer) listCollections(w http.ResponseWriter, r *http.Request) error {
	cols, err := s.kb.ListCollections(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": cols})
	return nil
}

func (s *KBServer) deleteCollection(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return kb.NewValidationError("invalid collection id")
	}
	if err := s.kb.DeleteCollection(r.Context(), id); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	return nil
}

func (s *KBServer) uploadDocument(w http.ResponseWriter, r *http.Request) error {
	collectionID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return kb.NewValidationError("invalid collection id")
	}

	contentType := r.Header.Get("Content-Type")
	var title, content, sourceType, sourcePath string

	if strings.HasPrefix(contentType, "multipart/form-data") {
		file, header, err := r.FormFile("file")
		if err != nil {
			return kb.NewValidationError("missing file field")
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		sourceType = "file"
		sourcePath = header.Filename
		title = header.Filename
		content = string(data)
	} else {
		var req struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return kb.NewValidationError("invalid request body: " + err.Error())
		}
		if req.Content == "" {
			return kb.NewValidationError("content must not be empty")
		}
		title = req.Title
		content = req.Content
		sourceType = "text"
	}

	doc, err := s.kb.IngestDocument(r.Context(), collectionID, title, content, sourceType, sourcePath)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, doc)
	return nil
}

func (s *KBServer) getDocument(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return kb.NewValidationError("invalid document id")
	}
	doc, err := s.kb.GetDocument(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, doc)
	return nil
}

func (s *KBServer) deleteDocument(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return kb.NewValidationError("invalid document id")
	}
	if err := s.kb.DeleteDocument(r.Context(), id); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	return nil
}

func (s *KBServer) retryDocument(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return kb.NewValidationError("invalid document id")
	}
	doc, err := s.kb.RetryDocument(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, doc)
	return nil
}

func (s *KBServer) search(w http.ResponseWriter, r *http.Request) error {
	var req kb.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return kb.NewValidationError("invalid request body: " + err.Error())
	}
	resp, err := s.kb.Search(r.Context(), &req)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *KBServer) chat(w http.ResponseWriter, r *http.Request) error {
	var req kb.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return kb.NewValidationError("invalid request body: " + err.Error())
	}
	resp, err := s.kb.Chat(r.Context(), &req)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
