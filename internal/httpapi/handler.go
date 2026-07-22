// Package httpapi exposes the URL shortener over HTTP using the standard
// library's method+pattern routing (Go 1.22+), so there is no third-party
// router dependency.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"urlshortener/internal/shortener"
	"urlshortener/internal/storage"
	"urlshortener/internal/validate"
)

// maxBodyBytes caps request bodies to guard against oversized payloads.
const maxBodyBytes = 1 << 20 // 1 MiB

// recentClicksLimit is how many recent clicks the stats endpoint returns.
const recentClicksLimit = 20

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
	mux.HandleFunc("GET /api/links/{code}/stats", h.stats)
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

	// Record analytics best-effort: a failure here must not break the redirect.
	click := storage.Click{
		Referer:   r.Referer(),
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
	}
	if err := h.svc.RecordClick(r.Context(), code, click); err != nil {
		log.Printf("record click %q: %v", code, err)
	}

	http.Redirect(w, r, originalURL, http.StatusMovedPermanently)
}

type statsResponse struct {
	Code         string          `json:"code"`
	ShortURL     string          `json:"short_url"`
	OriginalURL  string          `json:"original_url"`
	IsCustom     bool            `json:"is_custom"`
	ClickCount   int64           `json:"click_count"`
	CreatedAt    time.Time       `json:"created_at"`
	LastAccessAt *time.Time      `json:"last_access_at,omitempty"`
	RecentClicks []storage.Click `json:"recent_clicks"`
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	st, err := h.svc.Stats(r.Context(), code, recentClicksLimit)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "short code not found")
			return
		}
		log.Printf("stats %q: %v", code, err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, statsResponse{
		Code:         st.Link.Code,
		ShortURL:     h.svc.ShortURL(st.Link.Code),
		OriginalURL:  st.Link.OriginalURL,
		IsCustom:     st.Link.IsCustom,
		ClickCount:   st.Link.ClickCount,
		CreatedAt:    st.Link.CreatedAt,
		LastAccessAt: st.Link.LastAccessAt,
		RecentClicks: st.RecentClicks,
	})
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
			"stats":    "GET /api/links/{code}/stats",
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

// clientIP extracts the best-effort client IP, honouring X-Forwarded-For when
// present (first hop) and falling back to the connection's remote address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
