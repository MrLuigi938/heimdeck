package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

// Compares the tile with a running evcc (e.g. evcc --demo); run with EVCC_URL=http://127.0.0.1:7070.
func TestEvccLive(t *testing.T) {
	u := os.Getenv("EVCC_URL")
	if u == "" {
		t.Skip("EVCC_URL not set")
	}
	in := &Instance{BaseURL: u, PublicURL: u, Values: map[string]string{}, Client: http.DefaultClient, Lang: "de"}
	w, err := evcc{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(w, "", " ")
	t.Log(string(b))
}
