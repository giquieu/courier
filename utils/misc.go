package utils

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// MapAsJSON serializes the given map as a JSON string
func MapAsJSON(m map[string]string) []byte {
	bytes, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return bytes
}

// JoinNonEmpty takes a vararg of strings and return the join of all the non-empty strings with a delimiter between them
func JoinNonEmpty(delim string, strings ...string) string {
	var buf bytes.Buffer
	for _, s := range strings {
		if s != "" {
			if buf.Len() > 0 {
				buf.WriteString(delim)
			}
			buf.WriteString(s)
		}
	}
	return buf.String()
}

// DecodeUTF8 is equivalent to .decode('utf-8', 'ignore') in Python
func DecodeUTF8(bytes []byte) string {
	s := string(bytes)
	if !utf8.ValidString(s) {
		v := make([]rune, 0, len(s))
		for i, r := range s {
			if r == utf8.RuneError {
				_, size := utf8.DecodeRuneInString(s[i:])
				if size == 1 {
					continue
				}
			}
			v = append(v, r)
		}
		s = string(v)
	}
	return s
}

// StringArrayContains returns whether a given string array contains the given element
func StringArrayContains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}