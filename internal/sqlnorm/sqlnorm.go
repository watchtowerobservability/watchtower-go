// Package sqlnorm is the Go port of services/worker/src/sqlnorm.rs: literal-free statement text
// plus its XXH3-64 hash as a decimal string, so SDK spans join agent DBM and the worker's
// wt.db.query_hash. Spec: docs/product/instrumentation/sql-normalization.md; contract:
// testdata/sql_normalize_vectors.json.
package sqlnorm

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zeebo/xxh3"
)

// MaxLen caps the scanned input (UTF-8 bytes, cut back to a whole character).
const MaxLen = 8192

const maxDollarTag = 32

// PG reports whether db.system.name uses the Postgres dialect ("identifiers", # operator).
func PG(system string) bool {
	return system == "postgresql" || system == "cockroachdb" || system == "redshift"
}

// isAlnum approximates Rust char::is_alphanumeric (Alphabetic or N*).
// shortcut: unicode.IsLetter misses Other_Alphabetic marks; fine for SQL, revisit if a vector fails.
func isAlnum(c rune) bool { return unicode.IsLetter(c) || unicode.IsNumber(c) }

func isWord(c rune) bool { return isAlnum(c) || c == '_' || c == '$' }

func isSpace(c rune) bool { return unicode.Is(unicode.White_Space, c) }

func isDigit(c rune) bool { return c >= '0' && c <= '9' }

func hasPrefix(cs []rune, j int, p []rune) bool {
	if len(cs)-j < len(p) {
		return false
	}
	for k, r := range p {
		if cs[j+k] != r {
			return false
		}
	}
	return true
}

// dollarQuoted returns the index after the $tag$...$tag$ string opening at i, or -1.
func dollarQuoted(cs []rune, i int) int {
	tagLen := -1
	for k := 0; k <= maxDollarTag && i+1+k < len(cs); k++ {
		if cs[i+1+k] == '$' {
			tagLen = k
			break
		}
	}
	if tagLen < 0 {
		return -1
	}
	tag := cs[i : i+tagLen+2]
	for _, c := range tag[1 : tagLen+1] {
		if !isAlnum(c) && c != '_' {
			return -1
		}
	}
	if isDigit(tag[1]) {
		return -1
	}
	for j := i + len(tag); j < len(cs); j++ {
		if hasPrefix(cs, j, tag) {
			return j + len(tag)
		}
	}
	return len(cs)
}

func capped(s string) string {
	if len(s) <= MaxLen {
		return s
	}
	end := MaxLen
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// Normalize returns the normalized text; pg selects the Postgres dialect.
func Normalize(stmt string, pg bool) string {
	cs := []rune(capped(stmt))
	var out strings.Builder
	space := false
	for i := 0; i < len(cs); {
		c := cs[i]
		var next rune
		if i+1 < len(cs) {
			next = cs[i+1]
		}
		switch {
		case isSpace(c):
			space = true
			i++
		case c == '/' && next == '*':
			depth := 1
			i += 2
			for i < len(cs) && depth > 0 {
				switch {
				case cs[i] == '/' && i+1 < len(cs) && cs[i+1] == '*':
					depth, i = depth+1, i+2
				case cs[i] == '*' && i+1 < len(cs) && cs[i+1] == '/':
					depth, i = depth-1, i+2
				default:
					i++
				}
			}
			space = true
		case (c == '-' && next == '-') || (c == '#' && !pg):
			for i < len(cs) && cs[i] != '\n' {
				i++
			}
			space = true
		default:
			if space && out.Len() > 0 {
				out.WriteByte(' ')
			}
			space = false
			i = token(cs, i, pg, &out)
		}
	}
	return collapseIn(out.String())
}

func token(cs []rune, i int, pg bool, out *strings.Builder) int {
	c := cs[i]
	if c == '\'' {
		i++
		for i < len(cs) {
			if cs[i] == '\'' {
				if i+1 >= len(cs) || cs[i+1] != '\'' {
					break
				}
				i++
			}
			i++
		}
		out.WriteByte('?')
		if i < len(cs) && cs[i-1] == '\\' { // ambiguous MySQL escape: mask the rest
			return len(cs)
		}
		return i + 1
	}
	if c == '"' || c == '`' {
		end := len(cs)
		for j := i + 1; j < len(cs); j++ {
			if cs[j] == c {
				end = j
				break
			}
		}
		body := cs[i+1 : end]
		ident := c == '"' && pg && end < len(cs) && len(body) >= 1 && len(body) <= 64
		for _, b := range body {
			if !ident {
				break
			}
			ident = isAlnum(b) || b == '_' || b == '$'
		}
		if ident {
			out.WriteString(string(cs[i : end+1]))
		} else {
			out.WriteByte('?')
		}
		return end + 1
	}
	if c == '$' {
		if i+1 < len(cs) && isDigit(cs[i+1]) {
			i++
			for i < len(cs) && isDigit(cs[i]) {
				i++
			}
			out.WriteByte('?')
			return i
		}
		if end := dollarQuoted(cs, i); end >= 0 {
			out.WriteByte('?')
			return end
		}
	}
	switch {
	case isDigit(c):
		for i < len(cs) && (isWord(cs[i]) || cs[i] == '.') {
			i++
		}
		out.WriteByte('?')
	case isWord(c):
		for i < len(cs) && isWord(cs[i]) {
			out.WriteRune(cs[i])
			i++
		}
	default:
		out.WriteRune(c)
		i++
	}
	return i
}

// collapseIn turns `in (?, ?)` into `in (?+)` on the UTF-8 bytes, like the reference (the word
// boundary reads the previous byte as Latin-1).
func collapseIn(s string) string {
	b := []byte(s)
	var out strings.Builder
	for i := 0; i < len(b); {
		atWord := i == 0 || !isWord(rune(b[i-1]))
		if atWord && len(b)-i >= 2 && (b[i]|0x20) == 'i' && (b[i+1]|0x20) == 'n' {
			if open, end, ok := inList(b, i+2); ok {
				out.WriteString(s[i:open])
				out.WriteString("(?+)")
				i = end
				continue
			}
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		out.WriteRune(r)
		i += n
	}
	return out.String()
}

func inList(b []byte, i int) (open, end int, ok bool) {
	skip := func() {
		for i < len(b) && b[i] == ' ' {
			i++
		}
	}
	skip()
	open = i
	if i >= len(b) || b[i] != '(' {
		return 0, 0, false
	}
	i++
	for {
		skip()
		if i >= len(b) || b[i] != '?' {
			return 0, 0, false
		}
		i++
		skip()
		if i >= len(b) {
			return 0, 0, false
		}
		switch b[i] {
		case ',':
			i++
		case ')':
			return open, i + 1, true
		default:
			return 0, 0, false
		}
	}
}

// Hash is XXH3-64 (seed 0) of the normalized text as an unsigned decimal string.
func Hash(normalized string) string {
	return strconv.FormatUint(xxh3.HashString(normalized), 10)
}

var (
	opRe     = regexp.MustCompile(`(?is)^\s*(?:with\b.*?\)\s*)?([a-z]+)`)
	targetRe = regexp.MustCompile(`(?i)\b(?:from|into|update|join|table)\s+((?:"[^"]+"|[\w$]+)(?:\.(?:"[^"]+"|[\w$]+))?)`)
)

// Summary returns the uppercased operation and the first table (quotes removed) of normalized
// text, like the Node SDK's querySummary. Either may be empty.
func Summary(normalized string) (op, table string) {
	if m := opRe.FindStringSubmatch(normalized); m != nil {
		op = strings.ToUpper(m[1])
	}
	if m := targetRe.FindStringSubmatch(normalized); m != nil {
		table = strings.ReplaceAll(m[1], `"`, "")
	}
	return op, table
}
