package integrations

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNorish(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	id := "0b5d1f0e-3c4b-4c55-9a0e-6f1f2d3c4b5a"
	f := newFake(t, map[string]any{
		"POST /api/v1/recipes/search": obj{"total": 42, "nextCursor": nil, "recipes": []obj{
			{"id": id, "name": "Käsespätzle", "image": "/recipes/" + id + "/abc.jpg", "servings": 4, "totalMinutes": 35},
		}},
		"GET /api/v1/groceries": []obj{{"isDone": false}, {"isDone": true}, {"isDone": false}},
		"GET /api/v1/planned-recipes/week": []obj{
			{"date": tomorrow, "slot": "Lunch", "sortOrder": 0, "recipeId": id, "recipeName": "Suppe", "recipeImage": nil},
			{"date": today, "slot": "Dinner", "sortOrder": 0, "recipeId": id, "recipeName": "Käsespätzle", "recipeImage": "/recipes/" + id + "/abc.jpg"},
		},
	}, func(r *http.Request) bool { return r.Header.Get("x-api-key") == "k" })
	in := f.instance(map[string]string{"apiKey": "k"})
	w, err := norish{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Rezepte"] != "42" || stats(w)["Einkaufsliste"] != "2" || stats(w)["Heute geplant"] != "1" {
		t.Fatalf("%v %+v", err, w)
	}
	if len(w.Items) != 1 || w.Items[0].Subtitle != "Abendessen" || !strings.Contains(w.Items[0].Image, "/image?p=") || w.Items[0].URL != "https://svc.example.com/recipes/"+id {
		t.Fatalf("items %+v", w.Items)
	}
	items, err := norish{}.Search(context.Background(), in, "spätzle")
	if err != nil || len(items) != 1 || items[0].Subtitle != "35 Min. · 4 Portionen" {
		t.Fatalf("search %v %+v", err, items)
	}
	if !strings.Contains(f.body("POST /api/v1/recipes/search"), `"search":"spätzle"`) {
		t.Fatalf("search body %s", f.body("POST /api/v1/recipes/search"))
	}
	if _, _, err := (norish{}).ImageRequest(context.Background(), in, "/api/v1/groceries"); err == nil {
		t.Fatal("non-image path accepted")
	}
}
