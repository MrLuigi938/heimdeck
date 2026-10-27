package integrations

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// feed shows the newest entries of one or more RSS or Atom feeds (news,
// blogs, release notes, YouTube channels …). Each feed is read at most
// every 15 minutes.

type feed struct{}

func init() { register(feed{}) }

func (feed) Def() Definition {
	return Definition{
		ID: "rss", Name: "RSS-Feed", Icon: "📰", Category: "Allgemein",
		Description: "Die neuesten Einträge von RSS- oder Atom-Feeds: Nachrichten, Blogs, Release Notes, YouTube-Kanäle",
		NoURL:       true, HasWidget: true,
		Fields: []Field{
			{Key: "feeds", Label: "Feeds", Type: "textarea", Required: true,
				Help: "Eine Feed-Adresse pro Zeile, z. B. https://www.tagesschau.de/index~rss2.xml oder https://github.com/MrLuigi938/heimdeck/releases.atom. Mehrere Feeds werden nach Datum gemischt."},
			{Key: "count", Label: "Einträge", Type: "select", Options: []string{"5", "10", "15", "20"}, Display: true},
		},
	}
}

type feedEntry struct {
	Title, Link, Source string
	At                  time.Time
}

// rss covers RSS 2.0, RSS 1.0 (RDF) and Atom with one structure.
type rssDoc struct {
	Title   string     `xml:"channel>title"`
	Items   []rssItem  `xml:"channel>item"`
	RDF     []rssItem  `xml:"item"` // RSS 1.0
	ATitle  string     `xml:"title"`
	Entries []atomItem `xml:"entry"`
}

type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	GUID    string `xml:"guid"`
	PubDate string `xml:"pubDate"`
	Date    string `xml:"date"` // dc:date
}

type atomItem struct {
	Title string `xml:"title"`
	Links []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
	Updated   string `xml:"updated"`
	Published string `xml:"published"`
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// plain turns a title that may contain HTML or entities into text.
func plain(s string) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, ""))
	return strings.Join(strings.Fields(s), " ")
}

func feedTime(values ...string) time.Time {
	layouts := []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
		"2 Jan 2006 15:04:05 -0700", "Mon, 02 Jan 2006 15:04 -0700", "2006-01-02T15:04:05Z07:00", "2006-01-02"}
	for _, v := range values {
		v = strings.TrimSpace(v)
		for _, l := range layouts {
			if t, err := time.Parse(l, v); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// latin1 reads feeds that are not in UTF-8 (ISO-8859-1 / Windows-1252 are
// the usual ones); other encodings are read as they are.
func latin1(_ string, r io.Reader) (io.Reader, error) {
	b, err := io.ReadAll(r)
	if err != nil || utf8.Valid(b) {
		return strings.NewReader(string(b)), err
	}
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return strings.NewReader(string(runes)), nil
}

func (feed) read(ctx context.Context, in *Instance, u string) ([]feedEntry, error) {
	return remember(in, "feed\x00"+u, 15*time.Minute, func() ([]feedEntry, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return nil, in.Errorf("ungültige Feed-Adresse: %s", u)
		}
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.5")
		req.Header.Set("User-Agent", "Heimdeck (RSS)")
		resp, err := in.Client.Do(req)
		if err != nil {
			return nil, friendlyNetErr(in.Lang, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, &HTTPError{Status: resp.StatusCode, Lang: in.Lang}
		}
		var doc rssDoc
		dec := xml.NewDecoder(io.LimitReader(resp.Body, 5<<20))
		dec.Strict = false
		dec.CharsetReader = latin1
		dec.Entity = xml.HTMLEntity
		if err := dec.Decode(&doc); err != nil {
			return nil, in.Errorf("%s ist kein RSS- oder Atom-Feed", u)
		}
		source := plain(doc.Title)
		if source == "" {
			source = plain(doc.ATitle)
		}
		var out []feedEntry
		for _, it := range append(doc.Items, doc.RDF...) {
			link := strings.TrimSpace(it.Link)
			if link == "" && strings.HasPrefix(it.GUID, "http") {
				link = strings.TrimSpace(it.GUID)
			}
			out = append(out, feedEntry{Title: plain(it.Title), Link: link, Source: source, At: feedTime(it.PubDate, it.Date)})
		}
		for _, e := range doc.Entries {
			link := ""
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					link = l.Href
					break
				}
			}
			out = append(out, feedEntry{Title: plain(e.Title), Link: strings.TrimSpace(link), Source: source, At: feedTime(e.Published, e.Updated)})
		}
		if len(out) == 0 && len(doc.Items)+len(doc.RDF)+len(doc.Entries) == 0 && source == "" {
			return nil, in.Errorf("%s ist kein RSS- oder Atom-Feed", u)
		}
		return out, nil
	})
}

func (f feed) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var urls []string
	for _, line := range strings.Split(in.V("feeds"), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			urls = append(urls, line)
		}
	}
	if len(urls) == 0 {
		return nil, in.Errorf("Keine Feed-Adresse angegeben")
	}
	if len(urls) > 10 {
		urls = urls[:10]
	}
	var all []feedEntry
	var errs []error
	for _, u := range urls {
		entries, err := f.read(ctx, in, u)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		all = append(all, entries...)
	}
	if len(all) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	count, _ := strconv.Atoi(in.V("count"))
	if count <= 0 {
		count = 5
	}
	w := &Widget{ItemsTitle: in.T("Neueste Einträge")}
	today := 0
	for _, e := range all {
		if !e.At.IsZero() && time.Since(e.At) < 24*time.Hour {
			today++
		}
	}
	w.Stats = []Stat{{Label: in.T("Neu (24 Std.)"), Value: in.Num(today)}}
	if len(urls) > 1 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Feeds"), Value: in.Num(len(urls))})
	}
	for i, e := range all {
		if i >= count {
			break
		}
		sub := e.Source
		if !e.At.IsZero() {
			if sub != "" {
				sub += " · "
			}
			sub += in.Since(e.At)
		}
		w.Items = append(w.Items, Item{Title: e.Title, Subtitle: sub, URL: e.Link})
	}
	for _, err := range errs {
		w.Alerts = append(w.Alerts, fmt.Sprint(err))
	}
	return w, nil
}
