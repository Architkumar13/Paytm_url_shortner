// Package httpapi exposes the URL shortener over HTTP using the standard
// library's method+pattern routing (Go 1.22+), so there is no third-party
// router dependency.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"urlshortener/internal/shortener"
	"urlshortener/internal/storage"
	"urlshortener/internal/validate"
)

// maxBodyBytes caps request bodies to guard against oversized payloads.
const maxBodyBytes = 1 << 20 // 1 MiB

// Handler wires HTTP requests to the shortener service.
type Handler struct {
	svc *shortener.Service
}

// NewRouter builds the fully-wired HTTP handler (routes + middleware).
func NewRouter(svc *shortener.Service) http.Handler {
	h := &Handler{svc: svc}
	mux := http.NewServeMux()

	// Fixed patterns are more specific than "/{code}", so ServeMux routes them
	// first; a custom alias can never shadow an API route.
	mux.HandleFunc("POST /shorten", h.shorten)
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /{$}", h.index) // exact root only
	mux.HandleFunc("GET /{code}", h.redirect)

	return Recover(Logging(mux))
}

type shortenRequest struct {
	URL   string `json:"url"`
	Alias string `json:"alias"`
}

type shortenResponse struct {
	Code        string    `json:"code"`
	ShortURL    string    `json:"short_url"`
	OriginalURL string    `json:"original_url"`
	CreatedAt   time.Time `json:"created_at"`
}

func (h *Handler) shorten(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req shortenRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	res, err := h.svc.Shorten(r.Context(), req.URL, req.Alias)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrAliasTaken):
			writeError(w, http.StatusConflict, "alias already taken")
		case isValidationError(err):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			log.Printf("shorten: %v", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	status := http.StatusCreated // 201 for a freshly created mapping
	if !res.Created {
		status = http.StatusOK // 200 when returning an existing mapping (dedup)
	}
	writeJSON(w, status, shortenResponse{
		Code:        res.Link.Code,
		ShortURL:    h.svc.ShortURL(res.Link.Code),
		OriginalURL: res.Link.OriginalURL,
		CreatedAt:   res.Link.CreatedAt,
	})
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	originalURL, err := h.svc.ResolveURL(r.Context(), code) // read-through cache
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "short code not found")
			return
		}
		log.Printf("resolve %q: %v", code, err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	http.Redirect(w, r, originalURL, http.StatusMovedPermanently)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "datastore unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) index(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "url-shortener",
		"endpoints": map[string]string{
			"shorten":  "POST /shorten {\"url\": \"...\", \"alias\": \"optional\"}",
			"redirect": "GET /{code}",
			"health":   "GET /healthz",
		},
	})
}

// isValidationError reports whether err originates from input validation, which
// the API surfaces as 400 Bad Request.
func isValidationError(err error) bool {
	var ve *validate.ValidationError
	return errors.As(err, &ve)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
