package server

import (
	"bytes"
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
)

// Favicons are the fallback for tiles without a known icon: the icon a
// service declares on its start page (<link rel="icon">, apple-touch-icon),
// otherwise /favicon.ico. They are cached per address, failures for an hour
// so offline services are not asked on every page load.

type favicon struct {
	at    time.Time
	data  []byte
	ctype string
}

const (
	faviconTTL     = 24 * time.Hour
	faviconMissTTL = time.Hour
	faviconMax     = 512 << 10
)

var (
	linkTagRe = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	attrRe    = regexp.MustCompile(`(?is)([a-z][a-z-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

// iconLinks returns the icon URLs declared in a page, best first: SVG and
// large icons before small ones; mask icons (single-colour outlines) are skipped.
func iconLinks(page []byte, base *url.URL) []string {
	type cand struct {
		href  string
		score int
	}
	var list []cand
	for _, tag := range linkTagRe.FindAll(page, -1) {
		attrs := map[string]string{}
		for _, m := range attrRe.FindAllSubmatch(tag, -1) {
			attrs[strings.ToLower(string(m[1]))] = html.UnescapeString(string(m[2]) + string(m[3]) + string(m[4]))
		}
		rel := " " + strings.ToLower(attrs["rel"]) + " "
		if !strings.Contains(rel, " icon ") && !strings.Contains(rel, "apple-touch-icon") || strings.TrimSpace(attrs["href"]) == "" {
			continue
		}
		u, err := base.Parse(strings.TrimSpace(attrs["href"]))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		score := 16
		if size, _, ok := strings.Cut(strings.ToLower(attrs["sizes"]), "x"); ok {
			score, _ = strconv.Atoi(size)
		}
		if strings.Contains(rel, "apple-touch-icon") {
			score = max(score, 180)
		}
		if attrs["type"] == "image/svg+xml" || strings.HasSuffix(strings.ToLower(u.Path), ".svg") || attrs["sizes"] == "any" {
			score = 1000
		}
		list = append(list, cand{u.String(), score})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	out := []string{}
	for _, c := range list {
		out = append(out, c.href)
	}
	return out
}

func getLimited(ctx context.Context, client *http.Client, u string, limit int64) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Heimdeck)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp, nil, errors.New(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("too large")
	}
	return resp, b, err
}

// imageType returns the content type of an icon, or "" if it is no image.
func imageType(header string, b []byte) string {
	header = strings.ToLower(strings.TrimSpace(strings.Split(header, ";")[0]))
	head := bytes.ToLower(b[:min(len(b), 1024)])
	if header == "image/svg+xml" || bytes.Contains(head, []byte("<svg")) && !bytes.Contains(head, []byte("<html")) {
		return "image/svg+xml"
	}
	sniffed := http.DetectContentType(b)
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	return ""
}

func fetchFavicon(ctx context.Context, base string, insecure bool) (favicon, error) {
	client := integrations.NewClient(insecure)
	baseURL, err := url.Parse(base)
	if err != nil || baseURL.Host == "" {
		return favicon{}, errors.New("no address")
	}
	var candidates []string
	if resp, page, err := getLimited(ctx, client, base, 256<<10); err == nil {
		candidates = iconLinks(page, resp.Request.URL)
		candidates = append(candidates, resp.Request.URL.ResolveReference(&url.URL{Path: "/favicon.ico"}).String())
	} else if ctx.Err() != nil {
		return favicon{}, err
	}
	candidates = append(candidates, baseURL.ResolveReference(&url.URL{Path: "/favicon.ico"}).String())
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c] || ctx.Err() != nil {
			continue
		}
		seen[c] = true
		resp, b, err := getLimited(ctx, client, c, faviconMax)
		if err != nil || len(b) == 0 {
			continue
		}
		if ct := imageType(resp.Header.Get("Content-Type"), b); ct != "" {
			return favicon{data: b, ctype: ct}, nil
		}
	}
	return favicon{}, errors.New("no icon")
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	t, code, err := s.accessibleTile(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	base := t.APIURL
	if base == "" {
		base = t.URL
	}
	key := base
	s.mu.Lock()
	f, ok := s.favicons[key]
	st, checked := s.latest[t.ID]
	offline := checked && !st.Online
	if !ok || time.Since(f.at) > faviconTTL || f.data == nil && time.Since(f.at) > faviconMissTTL {
		done, running := s.faviconFetch[key]
		if !running && !offline && base != "" {
			done = make(chan struct{})
			s.faviconFetch[key] = done
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				var f favicon
				func() {
					defer logging.Recover("Favicon", "adresse", base)
					f, _ = fetchFavicon(ctx, base, t.Insecure)
				}()
				f.at = time.Now()
				s.mu.Lock()
				s.favicons[key] = f
				delete(s.faviconFetch, key)
				s.mu.Unlock()
				close(done)
			}()
			running = true
		}
		s.mu.Unlock()
		if running {
			// Wait briefly; a slow service must not hold the browser's connections.
			select {
			case <-done:
			case <-time.After(4 * time.Second):
			case <-r.Context().Done():
			}
		}
		s.mu.Lock()
		f = s.favicons[key]
	}
	s.mu.Unlock()
	if f.data == nil {
		w.Header().Set("Cache-Control", "private, max-age=600")
		http.Error(w, "no icon", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", f.ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Write(f.data)
}
