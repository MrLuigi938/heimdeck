package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

type panicky struct{}

func (panicky) Def() integrations.Definition {
	return integrations.Definition{ID: "test-panic", Name: "Panic"}
}
func (panicky) Fetch(context.Context, *integrations.Instance) (*integrations.Widget, error) {
	var m map[string]int
	m["boom"]++ // nil map: panics like a careless integration would
	return nil, nil
}

func TestPanicInIntegrationDoesNotCrash(t *testing.T) {
	s := testServer(t)
	integrations.Register(panicky{})
	w, err := s.safeFetch(context.Background(), store.Tile{ID: 1, Name: "x", Integration: "test-panic"}, "de")
	if w != nil || err == nil || !strings.Contains(err.Error(), "Protokoll") {
		t.Fatalf("got %v, %v", w, err)
	}
}

func TestPanicInHandlerIsAnError(t *testing.T) {
	h := logRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/x", nil))
	if rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestLockWithin(t *testing.T) {
	s := testServer(t)
	if !lockWithin(s, 0) {
		t.Fatal("free mutex not taken")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if lockWithin(s, 0) {
		t.Fatal("held mutex reported as free")
	}
}
