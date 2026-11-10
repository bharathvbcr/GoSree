package annot

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// verbs lists every directive verb, in the order the unknown-directive message names them.
var verbs = []string{"unsafe", "iso", "imm", "guarded_by", "holds", "mub", "rob", "mut", "ref"}

// A comment that parseDirective does not accept but that a reader takes for a directive is a near
// miss, and it is reported, because a directive that silently does nothing is a check the author
// believes in and nobody runs. The rule, applied to each line of the comment:
//
//  1. strip one comment marker ("//", or "/*" and "*/"; on later lines of a block comment, a leading
//     "*"), then leading white space;
//  2. the rest must start with "gosree:" in any letter case;
//  3. cut a trailing "// note", as parseDirective does;
//  4. the word after the colon (letters, digits, '_' and '-') is then judged:
//     - no word at all, nothing after the colon: a near miss (gofmt rewrites "//gosree:" to "// gosree:");
//     - the colon is followed by white space ("// gosree: ..."): a near miss only when the word is a
//     verb exactly, ignoring case, and is followed by white space or the end of the line. Prose such as
//     "// gosree: this lock is held" or "// gosree: see docs" is left alone;
//     - the word follows the colon directly ("//Gosree:guardedby"): a near miss when it is within edit
//     distance (insertions, deletions, substitutions, transpositions) 2 of guarded_by, 1 of unsafe or
//     holds, or 0 of the three-letter verbs, ignoring case.

// nearMiss reports whether the comment text raw reads as a gosree directive. It returns the line that
// does, as written but for a trailing "// note", and the spelling it most likely meant.
func nearMiss(raw string) (written, meant string, ok bool) {
	var body string
	block := false
	switch {
	case strings.HasPrefix(raw, "//"):
		body = raw[2:]
	case strings.HasPrefix(raw, "/*"):
		body = strings.TrimSuffix(raw[2:], "*/")
		block = true
	default:
		return "", "", false
	}
	for i, line := range strings.Split(body, "\n") {
		if block && i > 0 {
			line = strings.TrimLeftFunc(line, unicode.IsSpace)
			line = strings.TrimPrefix(line, "*")
		}
		if s, ok := nearMissLine(line); ok {
			if i == 0 {
				return quoted(raw), s, true
			}
			return strings.TrimSpace(cutNote(line)), s, true
		}
	}
	return "", "", false
}

// cutNote drops a trailing "// note" from a line.
func cutNote(s string) string {
	if i := strings.Index(s, "//"); i >= 0 {
		return s[:i]
	}
	return s
}

func nearMissLine(line string) (string, bool) {
	s := strings.TrimLeftFunc(line, unicode.IsSpace)
	if !hasFoldPrefix(s, "gosree:") {
		return "", false
	}
	lead := Prefix
	s = s[len("gosree:"):]
	s = cutNote(s)
	r, _ := utf8.DecodeRuneInString(s)
	gap := s != "" && unicode.IsSpace(r)
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	n := 0
	for n < len(s) && isWordByte(s[n]) {
		n++
	}
	word, rest := s[:n], strings.TrimSpace(s[n:])
	if word == "" {
		if rest == "" {
			return lead + "<verb>", true
		}
		return "", false
	}
	verb, d := closestVerb(word)
	if gap {
		r, _ := utf8.DecodeRuneInString(s[n:])
		if d != 0 || (n < len(s) && !unicode.IsSpace(r)) {
			return "", false
		}
	} else if d > fuzz(verb) {
		return "", false
	}
	if rest != "" {
		return lead + verb + " " + rest, true
	}
	return lead + verb, true
}

// suggestVerb returns the verb an unknown verb most likely meant, or "".
func suggestVerb(word string) string {
	verb, d := closestVerb(word)
	if d > fuzz(verb) {
		return ""
	}
	return verb
}

// fuzz is how far a spelling may be from verb and still be taken for it.
func fuzz(verb string) int {
	switch {
	case len(verb) >= 9:
		return 2
	case len(verb) >= 5:
		return 1
	}
	return 0
}

// closestVerb returns the verb nearest to word, ignoring ASCII case, and its distance.
func closestVerb(word string) (string, int) {
	w := strings.ToLower(word)
	best, bestD := "", -1
	for _, v := range verbs {
		if d := editDistance(w, v); bestD < 0 || d < bestD {
			best, bestD = v, d
		}
	}
	return best, bestD
}

// editDistance is the optimal string alignment distance between a and b, in bytes: insertions,
// deletions, substitutions and transpositions of adjacent bytes each cost 1. Inputs are short (a
// word of a comment against a verb), and a word longer than every verb by more than the largest fuzz
// cannot match, so it is cut off.
func editDistance(a, b string) int {
	const maxWord = 16
	if len(a) > maxWord {
		return len(a)
	}
	rows := make([][]int, len(a)+1)
	for i := range rows {
		rows[i] = make([]int, len(b)+1)
		rows[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		rows[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d = min(d, rows[i-2][j-2]+1)
			}
			rows[i][j] = d
		}
	}
	return rows[len(a)][len(b)]
}

func isWordByte(c byte) bool {
	return c == '_' || c == '-' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// hasFoldPrefix reports whether s starts with the lower-case ASCII prefix p, ignoring ASCII case.
func hasFoldPrefix(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != p[i] {
			return false
		}
	}
	return true
}
