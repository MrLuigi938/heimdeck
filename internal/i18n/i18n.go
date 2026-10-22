// Package i18n translates the backend's user-facing texts.
//
// German is the source language: code writes German strings and T looks up
// the English version. A missing entry simply falls back to German.
package i18n

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	DE = "de"
	EN = "en"
)

// Normalize maps any language tag ("en-US", "de_DE", …) to a supported language.
func Normalize(lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en") {
		return EN
	}
	return DE
}

// T translates s and, with args, formats it like fmt.Sprintf.
func T(lang, s string, args ...any) string {
	if lang == EN {
		if e, ok := en[s]; ok {
			s = e
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

var (
	sourceOnce sync.Once
	source     map[string]string // English text -> German source
)

// Source returns the German original of a text shown in lang, so settings
// that name a label (e.g. hidden tile values) work in every language.
// Texts without a translation are their own source.
func Source(lang, s string) string {
	if lang != EN {
		return s
	}
	sourceOnce.Do(func() {
		source = make(map[string]string, len(en))
		// Several German texts can share one translation: take the smallest,
		// so the result does not depend on map order.
		for de, e := range en {
			if old, taken := source[e]; !taken || de < old {
				source[e] = de
			}
		}
	})
	if de, ok := source[s]; ok {
		return de
	}
	return s
}

// Num formats an integer with thousands separators (1.234 / 1,234).
func Num(lang string, n int) string {
	s := fmt.Sprint(n)
	if n < 1000 && n > -1000 {
		return s
	}
	sep := byte('.')
	if lang == EN {
		sep = ','
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && c != '-' && (len(s)-i)%3 == 0 {
			out = append(out, sep)
		}
		out = append(out, c)
	}
	return string(out)
}

// Bytes formats a size like "5,2 GB" (German) or "5.2 GB" (English).
func Bytes(lang string, b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", b, units[i])
	}
	s := fmt.Sprintf("%.1f %s", b, units[i])
	if lang != EN {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

var weekdays = map[string][]string{
	DE: {"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"},
	EN: {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
}

// Date formats an RFC 3339 timestamp as a short local date ("Mo 19.10. 04:00" / "Mon 10/19 04:00").
func Date(lang, iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	t = t.Local()
	day := weekdays[Normalize(lang)][t.Weekday()]
	var date string
	if lang == EN {
		date = fmt.Sprintf("%s %d/%d", day, t.Month(), t.Day())
	} else {
		date = fmt.Sprintf("%s %02d.%02d.", day, t.Day(), t.Month())
	}
	if t.Hour() == 0 && t.Minute() == 0 {
		return date
	}
	return fmt.Sprintf("%s %02d:%02d", date, t.Hour(), t.Minute())
}

// Since describes how long ago t was ("vor 3 Std." / "3 h ago").
func Since(lang string, t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return T(lang, "gerade eben")
	case d < time.Hour:
		return T(lang, "vor %d Min.", int(d.Minutes()))
	case d < 24*time.Hour:
		return T(lang, "vor %d Std.", int(d.Hours()))
	case d < 48*time.Hour:
		return T(lang, "gestern")
	}
	return T(lang, "vor %d Tagen", int(d.Hours()/24))
}
