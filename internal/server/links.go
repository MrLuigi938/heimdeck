package server

import (
	"strings"

	"github.com/heimdeck/heimdeck/internal/integrations"
)

// Links and pictures in live data come from the services – and from anyone
// who can send a webhook message or answer a custom API tile. Only web
// addresses and Heimdeck's own paths reach the browser, never javascript:
// or similar links that would run in Heimdeck when clicked.

func safeLink(u string) string {
	t := strings.TrimSpace(u)
	l := strings.ToLower(t)
	if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || (strings.HasPrefix(t, "/") && !strings.HasPrefix(t, "//")) {
		return t
	}
	return ""
}

func safeImage(u string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(u)), "data:image/") {
		return strings.TrimSpace(u)
	}
	return safeLink(u)
}

func safeItems(items []integrations.Item) []integrations.Item {
	for i := range items {
		items[i].URL = safeLink(items[i].URL)
		items[i].Image = safeImage(items[i].Image)
	}
	return items
}

func safeWidget(w *integrations.Widget) *integrations.Widget {
	if w != nil {
		w.Items = safeItems(w.Items)
	}
	return w
}
