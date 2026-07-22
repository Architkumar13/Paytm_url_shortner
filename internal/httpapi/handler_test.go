package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"urlshortener/internal/shortener"
	"urlshortener/internal/storage"
)

func newTestRouter() http.Handler {
	svc := shortener.New(storage.NewMemoryStore(), "http://short.test")
	return NewRouter(svc)
}

func do(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	return rec
}

func TestShortenAndRedirectRoundTrip(t *testing.T) {
	router := newTestRouter()

	rec := do(t, router, "POST", "/shorten", `{"url":"https://example.com/a/b?x=1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("shorten status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp shortenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code == "" {
		t.Fatal("empty code in response")
	}
	if resp.ShortURL != "http://short.test/"+resp.Code {
		t.Fatalf("short_url = %q", resp.ShortURL)
	}

	// Redirect round-trip.
	rr := do(t, router, "GET", "/"+resp.Code, "")
	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("redirect status = %d, want 301", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "https://example.com/a/b?x=1" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestShortenDuplicateReturns200SameCode(t *testing.T) {
	router := newTestRouter()
	const body = `{"url":"https://example.com/dup"}`

	first := do(t, router, "POST", "/shorten", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first.Code)
	}
	second := do(t, router, "POST", "/shorten", body)
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (dedup)", second.Code)
	}

	var a, b shortenResponse
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(second.Body.Bytes(), &b)
	if a.Code != b.Code {
		t.Fatalf("dedup returned different codes: %q vs %q", a.Code, b.Code)
	}
}

func TestShortenCustomAliasAndConflict(t *testing.T) {
	router := newTestRouter()

	rec := do(t, router, "POST", "/shorten", `{"url":"https://a.com","alias":"promo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("alias create status = %d, want 201", rec.Code)
	}
	var resp shortenResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Code != "promo" {
		t.Fatalf("code = %q, want promo", resp.Code)
	}

	// Redirect via the alias works.
	rr := do(t, router, "GET", "/promo", "")
	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("alias redirect status = %d, want 301", rr.Code)
	}

	// Reusing the alias for a different URL conflicts.
	conflict := do(t, router, "POST", "/shorten", `{"url":"https://b.com","alias":"promo"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", conflict.Code)
	}
}

func TestUnknownCodeReturns404(t *testing.T) {
	router := newTestRouter()
	rec := do(t, router, "GET", "/does-not-exist", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestShortenBadInputs(t *testing.T) {
	router := newTestRouter()

	if rec := do(t, router, "POST", "/shorten", `{"url":"javascript:alert(1)"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad scheme status = %d, want 400", rec.Code)
	}
	if rec := do(t, router, "POST", "/shorten", `{not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d, want 400", rec.Code)
	}
	if rec := do(t, router, "POST", "/shorten", `{"url":"https://ok.com","alias":"a b"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad alias status = %d, want 400", rec.Code)
	}
	if rec := do(t, router, "POST", "/shorten", `{"url":"https://ok.com","extra":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", rec.Code)
	}
}

func TestStatsReflectClicks(t *testing.T) {
	router := newTestRouter()

	rec := do(t, router, "POST", "/shorten", `{"url":"https://example.com/stats"}`)
	var resp shortenResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	const clicks = 3
	for i := 0; i < clicks; i++ {
		do(t, router, "GET", "/"+resp.Code, "")
	}

	st := do(t, router, "GET", "/api/links/"+resp.Code+"/stats", "")
	if st.Code != http.StatusOK {
		t.Fatalf("stats status = %d, want 200", st.Code)
	}
	var stats statsResponse
	if err := json.Unmarshal(st.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.ClickCount != clicks {
		t.Fatalf("click_count = %d, want %d", stats.ClickCount, clicks)
	}
	if len(stats.RecentClicks) != clicks {
		t.Fatalf("recent_clicks = %d, want %d", len(stats.RecentClicks), clicks)
	}

	// Stats for an unknown code is 404.
	if miss := do(t, router, "GET", "/api/links/nope/stats", ""); miss.Code != http.StatusNotFound {
		t.Fatalf("stats unknown status = %d, want 404", miss.Code)
	}
}

func TestHealthAndIndex(t *testing.T) {
	router := newTestRouter()
	if rec := do(t, router, "GET", "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", rec.Code)
	}
	if rec := do(t, router, "GET", "/", ""); rec.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200", rec.Code)
	}
}
