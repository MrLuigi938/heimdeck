package integrations

import (
	"fmt"
	"strconv"
	"strings"
)

// A small path language for the custom API tile:
//
//	data.items[0].name     object keys, array index (also data.items.0.name)
//	items[-1]              last element
//	items.#                number of elements (or characters of a string)
//	items[state=down]      only elements whose field equals the value (also !=, >, <)
//	items[state=down].#    … and how many there are
//	.  or  $               the whole response
func lookup(v any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" || path == "." || path == "$" {
		return v, true
	}
	path = strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
	for _, tok := range splitPath(path) {
		switch {
		case tok == "#":
			switch x := v.(type) {
			case []any:
				v = float64(len(x))
			case map[string]any:
				v = float64(len(x))
			case string:
				v = float64(len([]rune(x)))
			default:
				return nil, false
			}
		case strings.HasPrefix(tok, "[") && strings.HasSuffix(tok, "]"):
			inner := tok[1 : len(tok)-1]
			arr, ok := v.([]any)
			if !ok {
				return nil, false
			}
			if n, err := strconv.Atoi(inner); err == nil {
				if n < 0 {
					n += len(arr)
				}
				if n < 0 || n >= len(arr) {
					return nil, false
				}
				v = arr[n]
				continue
			}
			f, ok := parseFilter(inner)
			if !ok {
				return nil, false
			}
			out := []any{}
			for _, el := range arr {
				if f.match(el) {
					out = append(out, el)
				}
			}
			v = out
		default:
			switch x := v.(type) {
			case map[string]any:
				val, ok := x[tok]
				if !ok {
					return nil, false
				}
				v = val
			case []any:
				n, err := strconv.Atoi(tok)
				if err != nil {
					// items.name → the name of every element
					var out []any
					for _, el := range x {
						if m, ok := el.(map[string]any); ok {
							out = append(out, m[tok])
						}
					}
					v = out
					continue
				}
				if n < 0 {
					n += len(x)
				}
				if n < 0 || n >= len(x) {
					return nil, false
				}
				v = x[n]
			default:
				return nil, false
			}
		}
	}
	return v, true
}

// splitPath splits "a.b[0].c[x=1].#" into a, b, [0], c, [x=1], #.
func splitPath(p string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '.':
			flush()
		case '[':
			flush()
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				cur.WriteString(p[i:])
				i = len(p)
				continue
			}
			out = append(out, p[i:i+j+1])
			i += j
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

type filter struct {
	field, op, value string
}

func parseFilter(s string) (filter, bool) {
	for _, op := range []string{"!=", ">=", "<=", "=", ">", "<"} {
		if i := strings.Index(s, op); i > 0 {
			return filter{strings.TrimSpace(s[:i]), op, strings.Trim(strings.TrimSpace(s[i+len(op):]), `"'`)}, true
		}
	}
	return filter{}, false
}

func (f filter) match(el any) bool {
	v, ok := lookup(el, f.field)
	if !ok {
		return f.op == "!="
	}
	return compare(v, f.op, f.value)
}

// compare compares numerically when both sides are numbers, else as text.
func compare(v any, op, want string) bool {
	got := fmt.Sprint(v)
	if b, ok := v.(bool); ok {
		got = strconv.FormatBool(b)
	}
	if v == nil {
		got = "null"
	}
	a, errA := strconv.ParseFloat(got, 64)
	b, errB := strconv.ParseFloat(want, 64)
	if errA == nil && errB == nil {
		switch op {
		case "=":
			return a == b
		case "!=":
			return a != b
		case ">":
			return a > b
		case "<":
			return a < b
		case ">=":
			return a >= b
		case "<=":
			return a <= b
		}
	}
	switch op {
	case "=":
		return strings.EqualFold(got, want)
	case "!=":
		return !strings.EqualFold(got, want)
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	case []any:
		return float64(len(x)), true
	}
	return 0, false
}
