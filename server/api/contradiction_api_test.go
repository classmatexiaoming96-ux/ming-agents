package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ming-agents/server/memory"
)

// useTempMemoryVault points memory.VaultDir at a fresh temp dir for the test.
func useTempMemoryVault(t *testing.T) string {
	t.Helper()
	prev := memory.VaultDir
	dir := t.TempDir()
	memory.VaultDir = dir
	t.Cleanup(func() { memory.VaultDir = prev })
	return dir
}

// seedAPINote writes an active note as frontmatter directly into the vault.
func seedAPINote(t *testing.T, id, project, body string, score float64) {
	t.Helper()
	dir := filepath.Join(memory.VaultDir, "notes", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nid: " + id + "\ntype: decision\nproject: " + project +
		"\nstatus: active\nlayer: l2\npromotion_state: promoted\nscore: " +
		floatStr(score) + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
}

func floatStr(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// seedAPIPair plants a same-project polarity-flip pair with enough lexical
// overlap and score gap to be superseded.
func seedAPIPair(t *testing.T) {
	t.Helper()
	seedAPINote(t, "mem_pool_yes", "p", "always enable the database connection pooling layer for every service", 4.0)
	seedAPINote(t, "mem_pool_no0", "p", "never enable the database connection pooling layer for every service", 2.0)
}

func TestPhase8_APIConflictsList(t *testing.T) {
	useTempMemoryVault(t)
	t.Setenv("MEMORY_API_RATELIMIT_DISABLE", "1")
	seedAPIPair(t)

	srv := NewServer(nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/memory/conflicts?limit=10", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Total int `json:"total"`
		Items []struct {
			Source string `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 1 || len(body.Items) != 1 || body.Items[0].Source != "lexical" {
		t.Fatalf("conflicts body = %s, want one lexical pair", rec.Body.String())
	}
}

func TestPhase8_APIResolveRequiresHumanActor(t *testing.T) {
	useTempMemoryVault(t)
	t.Setenv("MEMORY_API_RATELIMIT_DISABLE", "1")
	seedAPIPair(t)
	srv := NewServer(nil, nil, nil, nil)

	post := func(bodyJSON string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/memory/resolve", bytes.NewBufferString(bodyJSON))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec.Code
	}

	// service actor → 401
	if code := post(`{"pair":["mem_pool_no0","mem_pool_yes"],"evict":true,"apply":true,"actor":{"kind":"service","name":"bot"}}`); code != http.StatusUnauthorized {
		t.Errorf("service actor status = %d, want 401", code)
	}
	// empty actor → 401
	if code := post(`{"pair":["mem_pool_no0","mem_pool_yes"],"evict":true,"apply":true,"actor":{}}`); code != http.StatusUnauthorized {
		t.Errorf("empty actor status = %d, want 401", code)
	}
	// human actor → 200
	if code := post(`{"pair":["mem_pool_no0","mem_pool_yes"],"evict":true,"apply":true,"actor":{"kind":"human","name":"alice"}}`); code != http.StatusOK {
		t.Errorf("human actor status = %d, want 200", code)
	}
}

func TestPhase8_APIResolveMissingPair(t *testing.T) {
	useTempMemoryVault(t)
	t.Setenv("MEMORY_API_RATELIMIT_DISABLE", "1")
	srv := NewServer(nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/memory/resolve", bytes.NewBufferString(`{"all":false}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing pair status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

func TestPhase8_APIUnsupersedeApplyRequiresReason(t *testing.T) {
	useTempMemoryVault(t)
	t.Setenv("MEMORY_API_RATELIMIT_DISABLE", "1")
	srv := NewServer(nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/memory/unsupersede",
		bytes.NewBufferString(`{"id":"mem_x","apply":true,"actor":{"kind":"human","name":"alice"}}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("apply without reason status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

func TestPhase8_APIResolveRateLimit(t *testing.T) {
	useTempMemoryVault(t)
	seedAPIPair(t)

	// Fresh write bucket at a fixed clock so refill doesn't leak between calls.
	prevBucket := memoryWriteLimiter
	prevNow := nowFunc
	fixed := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return fixed }
	memoryWriteLimiter = newTokenBucket(10, 5) // burst 5
	t.Cleanup(func() {
		memoryWriteLimiter = prevBucket
		nowFunc = prevNow
	})

	srv := NewServer(nil, nil, nil, nil)
	// Dry-run apply=false requests avoid state changes; the limiter still counts them.
	post := func() (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/api/memory/resolve",
			bytes.NewBufferString(`{"all":true}`))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec.Code, rec.Header().Get("Retry-After")
	}

	// Burst of 5 succeeds, the 6th is limited (no refill under the frozen clock).
	for i := 0; i < 5; i++ {
		if code, _ := post(); code == http.StatusTooManyRequests {
			t.Fatalf("request %d limited early", i+1)
		}
	}
	code, retry := post()
	if code != http.StatusTooManyRequests {
		t.Fatalf("6th request status = %d, want 429", code)
	}
	if retry == "" {
		t.Errorf("429 missing Retry-After header")
	}

	// Disabling the limiter lets requests through again.
	t.Setenv("MEMORY_API_RATELIMIT_DISABLE", "1")
	if code, _ := post(); code == http.StatusTooManyRequests {
		t.Errorf("request limited despite disable env")
	}
}
