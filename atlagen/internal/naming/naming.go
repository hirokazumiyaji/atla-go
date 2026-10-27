// Package naming derives Go identifiers (model, package, field and enum names)
// from database names and checks them for collisions.
package naming

import "strings"

// initialisms is the Go initialism list of generator-spec §3, keyed by upper case.
var initialisms = setOf(
	"ACL", "API", "ASCII", "CPU", "CSS", "DNS", "EOF", "GUID", "HTML", "HTTP", "HTTPS", "ID", "IP",
	"JSON", "LHS", "QPS", "RAM", "RHS", "RPC", "SLA", "SMTP", "SQL", "SSH", "TCP", "TLS", "TTL", "UDP", "UI", "UID", "UUID", "URI", "URL", "UTF8", "VM",
	"XML", "XMPP", "XSRF", "XSS",
)

// unchanging lists words that look plural but must not be singularized by the
// suffix rules (ported from atla's naming.py _UNCHANGING).
var unchanging = setOf(
	"series", "news", "data", "media", "species", "meta", "info", "sms", "ops",
	"status", "analysis", "basis", "crisis", "thesis", "diagnosis", "oasis",
	"bus", "gas", "alias", "atlas", "virus", "bonus", "campus", "class",
)

// irregularSingular maps irregular plurals to their singular (ported from
// atla's naming.py _IRREGULAR_SINGULAR).
var irregularSingular = map[string]string{
	"people":   "person",
	"children": "child",
	"men":      "man",
	"women":    "woman",
	"teeth":    "tooth",
	"feet":     "foot",
	"mice":     "mouse",
	"geese":    "goose",
	"criteria": "criterion",
	"indices":  "index",
	"matrices": "matrix",
	"vertices": "vertex",
	"leaves":   "leaf",
	"lives":    "life",
	"knives":   "knife",
	"wolves":   "wolf",
	"shelves":  "shelf",
	"halves":   "half",
	"loaves":   "loaf",
	"quizzes":  "quiz",
	"statuses": "status",
	// -ies that become -ie, not -y.
	"movies":    "movie",
	"cookies":   "cookie",
	"zombies":   "zombie",
	"smoothies": "smoothie",
	"calories":  "calorie",
	"ties":      "tie",
	"pies":      "pie",
	"lies":      "lie",
	"dies":      "die",
	// -ses / -uses / -ases.
	"buses":      "bus",
	"gases":      "gas",
	"aliases":    "alias",
	"campuses":   "campus",
	"viruses":    "virus",
	"bonuses":    "bonus",
	"atlases":    "atlas",
	"classes":    "class",
	"analyses":   "analysis",
	"crises":     "crisis",
	"theses":     "thesis",
	"hypotheses": "hypothesis",
	"diagnoses":  "diagnosis",
	"oases":      "oasis",
}

// reservedPackages are table-package names that get the suffix "tbl" (Plan
// Decision 6): Go keywords, predeclared identifiers and the packages that
// generated files import. The root package name is checked separately.
var reservedPackages = setOf(
	// Keywords.
	"break", "case", "chan", "const", "continue", "default", "defer", "else",
	"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
	"map", "package", "range", "return", "select", "struct", "switch", "type", "var",
	// Predeclared identifiers.
	"any", "bool", "byte", "comparable", "complex64", "complex128", "error",
	"float32", "float64", "int", "int8", "int16", "int32", "int64", "rune",
	"string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
	"true", "false", "iota", "nil",
	"append", "cap", "clear", "close", "complex", "copy", "delete", "imag",
	"len", "make", "max", "min", "new", "panic", "print", "println", "real", "recover",
	// Packages imported by generated code.
	"atla", "dialect", "enum", "sql", "driver", "time", "json", "context",
	// Names the Go toolchain does not treat as ordinary importable packages:
	// "main" cannot be imported, "internal" restricts its importers, "vendor"
	// holds vendored dependencies, and "init" cannot name an imported package.
	"main", "internal", "vendor", "init",
)

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

// Pascal converts a database name to an exported Go identifier. It splits on
// every non-ASCII-alphanumeric rune and on camelCase boundaries, upper-cases Go
// initialisms (and their plural "<INITIALISM>s"), and prefixes "X" when the
// result starts with a digit. A name with no ASCII letters or digits gives "".
func Pascal(s string) string {
	return join(words(s))
}

// Singular returns the lowercase English singular of word.
func Singular(word string) string {
	w := strings.ToLower(word)
	if s, ok := irregularSingular[w]; ok {
		return s
	}
	if unchanging[w] {
		return w
	}
	switch {
	case len(w) > 3 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case hasAnySuffix(w, "sses", "xes", "zes", "ches", "shes"):
		return w[:len(w)-2]
	case len(w) > 1 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	}
	return w
}

// PackageName returns the column-package name of a model: the lowercase model
// name, with the suffix "tbl" when it is reserved or equals rootPkg.
func PackageName(model, rootPkg string) string {
	p := strings.ToLower(model)
	if reservedPackages[p] || p == rootPkg {
		return p + "tbl"
	}
	return p
}

// modelName singularizes the last word of a table name and Pascal-cases it.
// A last word that is itself an initialism ("dns", "https") is kept as is.
func modelName(table string) string {
	ws := words(table)
	if n := len(ws); n > 0 && !initialisms[strings.ToUpper(ws[n-1])] {
		ws[n-1] = Singular(ws[n-1])
	}
	return join(ws)
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// words splits s into lowercase words at non-ASCII-alphanumeric runes and
// camelCase boundaries.
func words(s string) []string {
	var out []string
	for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return !isAlnum(r) }) {
		start := 0
		for i := 1; i < len(seg); i++ {
			if camelBoundary(seg, i) {
				out = append(out, strings.ToLower(seg[start:i]))
				start = i
			}
		}
		out = append(out, strings.ToLower(seg[start:]))
	}
	return out
}

// camelBoundary reports whether a new word starts at seg[i]: "userName" splits
// before "N", "HTTPStatus" before "S". A trailing plural "s" after an upper-case
// run ("userIDs") stays with the run.
func camelBoundary(seg string, i int) bool {
	prev, cur := rune(seg[i-1]), rune(seg[i])
	if !isUpper(cur) {
		return false
	}
	if isLower(prev) || isDigit(prev) {
		return true
	}
	if !isUpper(prev) || i+1 >= len(seg) || !isLower(rune(seg[i+1])) {
		return false
	}
	pluralRun := seg[i+1] == 's' && (i+2 == len(seg) || !isLower(rune(seg[i+2])))
	return !pluralRun
}

// join capitalizes and concatenates words, prefixing "X" when the result
// starts with a digit.
func join(ws []string) string {
	out := concat(ws)
	if out != "" && isDigit(rune(out[0])) {
		return "X" + out
	}
	return out
}

// concat capitalizes and concatenates words.
func concat(ws []string) string {
	var b strings.Builder
	for _, w := range ws {
		b.WriteString(capitalize(w))
	}
	return b.String()
}

func capitalize(w string) string {
	up := strings.ToUpper(w)
	if initialisms[up] {
		return up
	}
	if len(w) > 1 && strings.HasSuffix(w, "s") && initialisms[up[:len(up)-1]] {
		return up[:len(up)-1] + "s"
	}
	return up[:1] + w[1:]
}

func isAlnum(r rune) bool { return isLower(r) || isUpper(r) || isDigit(r) }
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }
func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }
