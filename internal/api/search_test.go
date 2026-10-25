package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
)

// The search box maps over every list, so an empty one must be [] and not null.
func TestSearchEmptyListsAreArrays(t *testing.T) {
	s := &Server{DB: openTestDB(t), VideoRoot: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := doReq(s.Router(), http.MethodGet, "/api/search?q=nothing", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tags", "categories", "clips"} {
		if string(body[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, body[key])
		}
	}
}
