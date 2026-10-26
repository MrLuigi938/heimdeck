package integrations

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Norish: self-hosted recipe app with meal planning and a shared grocery list.
// Public API under /api/v1 with an API key (docs.norish.dev/reference/api).
// Keys are limited to 500 requests per hour, so the tile refreshes slowly.

type norish struct{}

func init() { register(norish{}) }

func (norish) Def() Definition {
	return Definition{
		ID: "norish", Name: "Norish", Icon: norishIcon, Category: "Haushalt",
		Description: "Heute geplante Gerichte, offene Einkäufe, Anzahl der Rezepte und Rezeptsuche",
		Setup: []string{
			"In Norish unter Einstellungen → Benutzer → API-Schlüssel einen Schlüssel erstellen und hier eintragen.",
			"Als lokale Adresse die Adresse von Norish eintragen, z. B. http://<IP>:3000.",
		},
		Fields: []Field{
			{Key: "apiKey", Label: "API-Schlüssel", Type: "password", Required: true, Secret: true,
				Help: "Norish → Einstellungen → Benutzer → API-Schlüssel → Schlüssel erstellen"},
		},
		HasWidget: true, HasSearch: true, Images: []string{"norish"}, Port: 3000,
		Refresh: 120,
	}
}

type norishRecipe struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	Servings     int    `json:"servings"`
	TotalMinutes *int   `json:"totalMinutes"`
	PrepMinutes  *int   `json:"prepMinutes"`
	CookMinutes  *int   `json:"cookMinutes"`
}

type norishSearch struct {
	Recipes []norishRecipe `json:"recipes"`
	Total   int            `json:"total"`
}

type norishPlanned struct {
	Date        string `json:"date"`
	Slot        string `json:"slot"`
	SortOrder   int    `json:"sortOrder"`
	RecipeID    string `json:"recipeId"`
	RecipeName  string `json:"recipeName"`
	RecipeImage string `json:"recipeImage"`
}

func (norish) req(in *Instance, method, path string, body any) request {
	return request{method: method, path: "/api/v1" + path, body: body, headers: map[string]string{"x-api-key": in.V("apiKey")}}
}

func (n norish) search(ctx context.Context, in *Instance, query string, limit int) (*norishSearch, error) {
	body := map[string]any{"limit": limit, "cursor": 0}
	if query != "" {
		body["search"] = query
		body["searchFields"] = []string{"title", "ingredients", "tags"}
		body["sortMode"] = "titleAsc"
	}
	var res norishSearch
	err := in.do(ctx, n.req(in, "POST", "/recipes/search", body), &res)
	return &res, err
}

func (n norish) Test(ctx context.Context, in *Instance) error {
	_, err := n.search(ctx, in, "", 1)
	return err
}

var norishSlots = map[string]string{"Breakfast": "Frühstück", "Lunch": "Mittagessen", "Dinner": "Abendessen", "Snack": "Snack"}
var norishSlotOrder = map[string]int{"Breakfast": 0, "Lunch": 1, "Dinner": 2, "Snack": 3}

var norishImageRe = regexp.MustCompile(`^/recipes/[0-9a-fA-F-]{36}/[A-Za-z0-9._-]+$`)

func (norish) image(in *Instance, path string) string {
	if norishImageRe.MatchString(path) {
		return in.Proxy(path)
	}
	return ""
}

func (n norish) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !norishImageRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, map[string]string{"x-api-key": in.V("apiKey")}, nil
}

func (n norish) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	all, err := n.search(ctx, in, "", 1)
	if err != nil {
		return nil, err
	}
	var groceries []struct {
		IsDone bool `json:"isDone"`
	}
	if err := in.do(ctx, n.req(in, "GET", "/groceries", nil), &groceries); err != nil {
		return nil, err
	}
	open, done := 0, 0
	for _, g := range groceries {
		if !g.IsDone {
			open++
		} else {
			done++
		}
	}
	// The week (Monday to Sunday) shows today's meals and, if there are none, the next ones.
	var week []norishPlanned
	if err := in.do(ctx, n.req(in, "GET", "/planned-recipes/week", nil), &week); err != nil {
		return nil, err
	}
	today := time.Now().Format("2006-01-02")
	sort.SliceStable(week, func(i, j int) bool {
		a, b := week[i], week[j]
		if a.Date[:min(10, len(a.Date))] != b.Date[:min(10, len(b.Date))] {
			return a.Date < b.Date
		}
		if norishSlotOrder[a.Slot] != norishSlotOrder[b.Slot] {
			return norishSlotOrder[a.Slot] < norishSlotOrder[b.Slot]
		}
		return a.SortOrder < b.SortOrder
	})
	var todays, upcoming []norishPlanned
	for _, p := range week {
		switch day := p.Date[:min(10, len(p.Date))]; {
		case day == today:
			todays = append(todays, p)
		case day > today:
			upcoming = append(upcoming, p)
		}
	}

	w := &Widget{Stats: []Stat{
		{Label: in.T("Heute geplant"), Value: in.Num(len(todays))},
		{Label: in.T("Einkaufsliste"), Value: in.Num(open), Tone: toneIf(open > 0, "warn")},
		{Label: in.T("Rezepte"), Value: in.Num(all.Total)},
		{Label: in.T("Diese Woche geplant"), Value: in.Num(len(week))},
		{Label: in.T("Schon eingekauft"), Value: in.Num(done)},
	}}
	list, showDate := todays, false
	w.ItemsTitle = in.T("Heute auf dem Plan")
	if len(list) == 0 && len(upcoming) > 0 {
		list, showDate = upcoming, true
		w.ItemsTitle = in.T("Als Nächstes geplant")
	}
	for _, p := range list[:min(4, len(list))] {
		sub := in.T(firstNonEmpty(norishSlots[p.Slot], p.Slot))
		if showDate {
			sub = in.Date(p.Date) + " · " + sub
		}
		w.Items = append(w.Items, Item{Title: firstNonEmpty(p.RecipeName, in.T("Rezept")), Subtitle: sub,
			Image: n.image(in, p.RecipeImage), URL: in.Link("/recipes/" + p.RecipeID)})
	}
	return w, nil
}

// Search finds recipes by title, ingredients and tags.
func (n norish) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	res, err := n.search(ctx, in, strings.TrimSpace(query), 8)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, r := range res.Recipes {
		var sub []string
		minutes := r.TotalMinutes
		if minutes == nil && r.PrepMinutes != nil && r.CookMinutes != nil {
			m := *r.PrepMinutes + *r.CookMinutes
			minutes = &m
		}
		if minutes != nil && *minutes > 0 {
			sub = append(sub, fmt.Sprintf("%d %s", *minutes, in.T("Min.")))
		}
		if r.Servings > 0 {
			sub = append(sub, in.T("%d Portionen", r.Servings))
		}
		out = append(out, Item{Title: r.Name, Subtitle: strings.Join(sub, " · "), Image: n.image(in, r.Image), URL: in.Link("/recipes/" + r.ID)})
	}
	return out, nil
}

// norishIcon is Norish's favicon (github.com/norish-recipes/norish); it is not part of the dashboard-icons collection.
const norishIcon = "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZlcnNpb249IjEuMSIgeG1sbnM6eGxpbms9Imh0dHA6Ly93d3cudzMub3JnLzE5OTkveGxpbmsiIHdpZHRoPSIxMDAwIiBoZWlnaHQ9IjEwMDAiPjxnIGNsaXAtcGF0aD0idXJsKCNTdmdqc0NsaXBQYXRoMTE4OCkiPjxyZWN0IHdpZHRoPSIxMDAwIiBoZWlnaHQ9IjEwMDAiIGZpbGw9IiNmOGYzZTciPjwvcmVjdD48ZyB0cmFuc2Zvcm09Im1hdHJpeCgxMy4zMzI0NDQ1MDM2OTk3NTMsMCwwLDEzLjMzMjQ0NDUwMzY5OTc1MywyMjUuMTcxNjU1MjIyOTg1MTgsMjAwKSI+PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHhtbG5zOnhsaW5rPSJodHRwOi8vd3d3LnczLm9yZy8xOTk5L3hsaW5rIiB2aWV3Qm94PSIwIDAgNDEuMjI3IDQ1LjAwMyIgd2lkdGg9IjQxLjIyNyIgaGVpZ2h0PSI0NS4wMDMiPjxkZWZzPiA8c3R5bGU+IC5jbHMtMXtmaWxsOiMzMzY2NDB9IDwvc3R5bGU+IDxjbGlwUGF0aCBpZD0iU3ZnanNDbGlwUGF0aDExODgiPjxyZWN0IHdpZHRoPSIxMDAwIiBoZWlnaHQ9IjEwMDAiIHg9IjAiIHk9IjAiIHJ4PSI1MDAiIHJ5PSI1MDAiPjwvcmVjdD48L2NsaXBQYXRoPjwvZGVmcz48ZyBpZD0iQXNzZXRfMiIgZGF0YS1uYW1lPSJBc3NldCAyIj4gPGc+IDxwYXRoIGQ9Ik00MS4yMjcgMS4yMyBMNDEuMjI3IDExLjA2NCBDNDEuMjI3IDExLjk5IDQxLjA0NCAxMi44OTQgNDAuNjk3IDEzLjczNCBDNDAuMzQ4IDE0LjU3NCAzOS44MzIgMTUuMzUgMzkuMTcyIDE2LjAwNyBDMzcuODggMTcuMjkxIDM3LjE3MyAxOS4wNDYgMzcuMTQ2IDIwLjg2MSBMMzcuMTQ2IDQ0LjUxNiBDMzcuMTQ2IDQ0Ljc4NSAzNi45MjggNDUuMDAzIDM2LjY1OSA0NS4wMDMgTDM2LjAwNiA0NS4wMDMgQzM1Ljg2NSA0NS4wMDMgMzUuNzI5IDQ0Ljk0MiAzNS42MzggNDQuODM2IEMzNS42MzggNDQuODM2IDEwLjQxMiAxNC4yNTIgMTAuNDEyIDE0LjI1MyBMMTAuNDEyIDM2LjE4NyBDMTAuNDEyIDM3LjAyMSAxMC40NzMgMzcuODY0IDEwLjU5NSAzOC42OSBMMTAuNTk1IDM4LjY5MyBDMTAuNzYxIDM5LjYzIDEwLjk1MiA0MC40MjUgMTEuMTYzIDQxLjA2IEMxMS4zNjMgNDEuNjcgMTEuNjU2IDQyLjI2MSAxMi4wMjggNDIuODA3IEMxMi4xOTcgNDMuMDMxIDEyLjM2MyA0My4yMzkgMTIuNTA3IDQzLjQxMSBDMTIuNjI5IDQzLjU1NSAxMi43NjUgNDMuNjc3IDEyLjkxNSA0My43NzcgQzEzLjA3IDQzLjg4MyAxMy4yNjQgNDMuOTc0IDEzLjQ4NiA0NC4wNTQgQzEzLjcwOCA0NC4xMzIgMTMuODQ5IDQ0LjM2MiAxMy44MDggNDQuNTk1IEMxMy43NjkgNDQuODMxIDEzLjU3MiA0NS4wMDMgMTMuMzM0IDQ1LjAwMyBMMS40ODggNDUuMDAzIEMxLjI5MSA0NS4wMDMgMS4wOTggNDQuODk4IDEuMDIgNDQuNzE3IEMwLjkwNyA0NC40NTMgMS4wMzUgNDQuMTYzIDEuMjg5IDQ0LjA2MyBDMS41NDEgNDMuOTc0IDEuNzMzIDQzLjg4IDEuODg1IDQzLjc3NyBDMi4wMzUgNDMuNjc3IDIuMTcxIDQzLjU1NSAyLjI5IDQzLjQxNCBDMi40MzcgNDMuMjM5IDIuNjAzIDQzLjAzMSAyLjc4OSA0Mi43ODggQzIuNzg5IDQyLjc4NyAyLjc4OSA0Mi43ODcgMi43ODkgNDIuNzg3IEMzLjE0NCA0Mi4yNjMgMy40MzUgNDEuNjczIDMuNjQgNDEuMDYgQzMuODQ4IDQwLjQyNSA0LjAzOSAzOS42MyA0LjIwNSAzOC42OTMgQzQuMzMgMzcuODU2IDQuMzk0IDM3LjAwNyA0LjM5NCAzNi4xNjcgTDQuMzk0IDEwLjg3MSBDNC4zOTQgMTAuODcgNC4zOTQgMTAuODcgNC4zOTQgMTAuODcgQzMuNDcxIDkuODI1IDIuNDIzIDguODk2IDEuMjc4IDguMTA2IEwwLjIxMSA3LjM3MiBDMC4wMzYgNy4yNTIgLTAuMDQyIDcuMDMxIDAuMDIyIDYuODI4IEMwLjA4NiA2LjYyNiAwLjI3MiA2LjQ4NyAwLjQ4NSA2LjQ4NyBMMTUuOTgyIDYuNDg3IEMxNi4xMjQgNi40ODcgMTYuMjU3IDYuNTQ4IDE2LjM1MSA2LjY1MyBMMzMuMDEzIDI1LjkyNCBDMzMuMDEzIDI1LjkyNSAzMy4wMTUgMjUuOTI0IDMzLjAxNSAyNS45MjMgTDMzLjAxNSAyMC45MjIgQzMzLjAxNSAxOS45OTkgMzIuODMyIDE5LjA5MyAzMi40ODMgMTguMjU1IEMzMi4xMzMgMTcuNDEzIDMxLjYyMSAxNi42MzYgMzAuOTYxIDE1Ljk3OSBDMjkuNjQxIDE0LjY2OCAyOC45MzEgMTIuODY2IDI4LjkzMSAxMS4wMDkgTDI4LjkzMSAxLjIyOSBDMjguOTMxIDAuNTUgMjkuNDgyIDAgMzAuMTYxIDAgTDMwLjE2MSAwIEMzMC44MzcgMCAzMS4zOSAwLjU1MyAzMS4zOSAxLjIyOSBMMzEuMzkgOS4xNTQgQzMxLjM5IDkuNzYxIDMxLjgwOSAxMC4zMTYgMzIuNDA4IDEwLjQxNSBDMzIuODMzIDEwLjQ4NSAzMy4yMTkgMTAuMzM4IDMzLjQ4OSAxMC4wNzIgQzMzLjcxMSA5Ljg1IDMzLjg0OSA5LjU0NSAzMy44NDkgOS4yMDQgTDMzLjg0OSAxLjI3MSBDMzMuODQ5IDAuNzIxIDM0LjE5MSAwLjIwNyAzNC43MTggMC4wNTIgQzM1LjE5IC0wLjA4NiAzNS42NDkgMC4wNjIgMzUuOTQ4IDAuMzYgQzM2LjE3IDAuNTgyIDM2LjMwOCAwLjg5IDM2LjMwOCAxLjIyOCBMMzYuMzA4IDkuMTU0IEMzNi4zMDggOS43NjEgMzYuNzI3IDEwLjMxNiAzNy4zMjYgMTAuNDE1IEMzNy43NTEgMTAuNDg1IDM4LjEzNyAxMC4zMzggMzguNDA3IDEwLjA3MiBDMzguNjI5IDkuODUgMzguNzY4IDkuNTQ1IDM4Ljc2OCA5LjIwNCBMMzguNzY4IDEuMjI5IEMzOC43NjggMC41NSAzOS4zMTggMCAzOS45OTcgMCBMMzkuOTk3IDAgQzQwLjY3NiAwIDQxLjIyNyAwLjU1IDQxLjIyNyAxLjIzIFoiIGNsYXNzPSJjbHMtMSI+PC9wYXRoPiA8L2c+IDwvZz48L3N2Zz48L2c+PC9nPjwvc3ZnPg=="
