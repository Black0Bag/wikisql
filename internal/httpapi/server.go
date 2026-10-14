package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Black0Bag/wikisql/internal/auth"
	"github.com/Black0Bag/wikisql/internal/kb"
	"github.com/Black0Bag/wikisql/internal/store"
	"github.com/Black0Bag/wikisql/internal/table"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	tables   *table.Service
	store    *store.Store
	logger   *slog.Logger
	kbRoutes func(r chi.Router)
	authMgr  *auth.Manager
	mcpHdl   http.Handler
	security SecurityOptions
}

type SecurityOptions struct {
	MaxBodyBytes int64
	GlobalRPM    int
	LoginRPM     int
}

func New(tables *table.Service, s *store.Store, logger *slog.Logger) *Server {
	return &Server{
		tables: tables,
		store:  s,
		logger: logger,
	}
}

func (s *Server) SetAuth(m *auth.Manager)      { s.authMgr = m }
func (s *Server) SetMCP(h http.Handler)        { s.mcpHdl = h }
func (s *Server) SetSecurity(o SecurityOptions) { s.security = o }

type apiHandler func(w http.ResponseWriter, r *http.Request) error

func (h apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		renderError(w, r, err)
	}
}

func renderError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	var ve *table.ValidationError
	var ke *kb.ValidationError
	switch {
	case errors.Is(err, errNotFound):
		status = http.StatusNotFound
	case errors.As(err, &ve) || errors.As(err, &ke):
		status = http.StatusBadRequest
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error":  err.Error(),
		"status": status,
	})
}

var errNotFound = errors.New("not found")

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(SecurityHeaders)
	r.Use(MaxBody(s.security.MaxBodyBytes))
	r.Use(RateLimit(s.security.GlobalRPM))

	// 公开端点
	r.Get("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs", http.StatusFound)
	}))
	r.Get("/health", apiHandler(s.handleHealth).ServeHTTP)
	r.Get("/openapi.json", http.HandlerFunc(ServeOpenAPI))
	r.Get("/docs", http.HandlerFunc(ServeDocs))
	r.Get("/docs/", http.HandlerFunc(ServeDocs))

	// 受保护路由的公共构造
	protected := func(r chi.Router) {
		if s.authMgr != nil {
			r.Use(s.authMgr.Middleware())
		}
	}

	// 认证
	r.Route("/api/v1", func(r chi.Router) {
		loginRPM := s.security.LoginRPM
		if loginRPM <= 0 {
			loginRPM = 5
		}
		if s.authMgr != nil && !s.authMgr.IsAPIKeyMode() {
			r.With(RateLimitStrict(loginRPM)).Post("/auth/login", s.authMgr.LoginHandler().ServeHTTP)
		}

		r.Group(func(r chi.Router) {
			protected(r)
			r.Get("/tables", apiHandler(s.handleListTables).ServeHTTP)
			r.Get("/tables/{name}/schema", apiHandler(s.handleTableSchema).ServeHTTP)
			r.Post("/query", apiHandler(s.handleQuery).ServeHTTP)

			r.Route("/tables/{name}/rows", func(r chi.Router) {
				r.Get("/", apiHandler(s.handleListRows).ServeHTTP)
				r.Post("/", apiHandler(s.handleInsertRow).ServeHTTP)
				r.Get("/{rowid}", apiHandler(s.handleGetRow).ServeHTTP)
				r.Put("/{rowid}", apiHandler(s.handleUpdateRow).ServeHTTP)
				r.Delete("/{rowid}", apiHandler(s.handleDeleteRow).ServeHTTP)
			})

			if s.kbRoutes != nil {
				s.kbRoutes(r)
			}
		})
	})

	// MCP 端点（与 REST 同一鉴权）
	if s.mcpHdl != nil {
		r.Group(func(r chi.Router) {
			protected(r)
			r.Handle("/mcp", s.mcpHdl)
		})
	}

	return r
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	return nil
}

func (s *Server) handleListTables(w http.ResponseWriter, r *http.Request) error {
	tables, err := s.tables.ListTables(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
	return nil
}

func (s *Server) handleTableSchema(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	schema, err := s.tables.GetSchema(r.Context(), name)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"table": name, "schema": schema})
	return nil
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) error {
	var req table.QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return table.NewValidationError("invalid request body: " + err.Error())
	}
	resp, err := s.tables.Execute(r.Context(), &req)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) handleListRows(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	req := &table.QueryRequest{
		Table:     name,
		Operation: "select",
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			req.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			req.Offset = n
		}
	}
	if v := r.URL.Query().Get("order_by"); v != "" {
		req.OrderBy = v
	}
	resp, err := s.tables.Execute(r.Context(), req)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) handleGetRow(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	rowID := chi.URLParam(r, "rowid")
	req := &table.QueryRequest{
		Table:     name,
		Operation: "select",
		Where:     map[string]any{"rowid": rowID},
	}
	resp, err := s.tables.Execute(r.Context(), req)
	if err != nil {
		return err
	}
	if len(resp.Rows) == 0 {
		return errNotFound
	}
	writeJSON(w, http.StatusOK, resp.Rows[0])
	return nil
}

func (s *Server) handleInsertRow(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var values map[string]any
	if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
		return table.NewValidationError("invalid request body: " + err.Error())
	}
	req := &table.QueryRequest{
		Table:     name,
		Operation: "insert",
		Values:    values,
	}
	resp, err := s.tables.Execute(r.Context(), req)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, resp)
	return nil
}

func (s *Server) handleUpdateRow(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	rowID := chi.URLParam(r, "rowid")
	var values map[string]any
	if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
		return table.NewValidationError("invalid request body: " + err.Error())
	}
	req := &table.QueryRequest{
		Table:     name,
		Operation: "update",
		Values:    values,
		Where:     map[string]any{"rowid": rowID},
	}
	resp, err := s.tables.Execute(r.Context(), req)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) handleDeleteRow(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	rowID := chi.URLParam(r, "rowid")
	req := &table.QueryRequest{
		Table:     name,
		Operation: "delete",
		Where:     map[string]any{"rowid": rowID},
	}
	resp, err := s.tables.Execute(r.Context(), req)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

func (s *Server) RegisterKB(svc *kb.Service) {
	kbSrv := NewKB(svc)
	s.kbRoutes = kbSrv.Routes
}
