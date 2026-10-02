// SPDX-License-Identifier: AGPL-3.0-or-later

package smtp

import (
	"strings"
	"unicode/utf8"
)

// Header lines should stay within 78 characters (RFC 5322 section 2.1.1).
// An encoded word may be at most 75 characters, and a line holding one at most
// 76 (RFC 2047 section 2).
const (
	foldWidth    = 78
	encodedWidth = 75
	wordPrefix   = "=?utf-8?q?"
	wordSuffix   = "?="
)

// encodeSubject renders a subject header value that survives transport and
// decodes back to exactly the subject given.
//
// Plain printable ASCII is kept readable and only folded at its spaces. Any
// other text is RFC 2047 Q-encoded. So is ASCII that contains "=?", which a
// mail reader would otherwise try to decode, and ASCII with a word too long
// to fold. The standard library's encoder leaves both of those untouched, so
// the encoding is done here.
func encodeSubject(subject string) string {
	if plainSubject(subject) {
		return foldPlain(subject)
	}
	return strings.Join(encodedWords(subject), "\r\n ")
}

func plainSubject(subject string) bool {
	if strings.Contains(subject, "=?") {
		return false
	}
	word := 0
	for i := 0; i < len(subject); i++ {
		c := subject[i]
		if c < 0x20 || c > 0x7e {
			return false
		}
		if c == ' ' {
			word = 0
			continue
		}
		word++
		// "Subject: " takes nine columns of the first line.
		if word > foldWidth-len("Subject: ") {
			return false
		}
	}
	return true
}

// foldPlain inserts a line break before a space wherever a line would
// otherwise run past foldWidth. Unfolding removes only the CRLF, so the text is
// unchanged once read back.
func foldPlain(subject string) string {
	var out strings.Builder
	column := len("Subject: ")
	for i, word := range strings.Split(subject, " ") {
		if i > 0 {
			if column+1+len(word) > foldWidth {
				out.WriteString("\r\n")
				column = 0
			}
			out.WriteByte(' ')
			column++
		}
		out.WriteString(word)
		column += len(word)
	}
	return out.String()
}

// encodedWords splits subject into Q-encoded words, never dividing a UTF-8
// sequence between two words, as RFC 2047 section 5 requires. Bytes that are
// not valid UTF-8 are carried through one at a time.
func encodedWords(subject string) []string {
	var words []string
	var payload strings.Builder
	// The first word shares its line with "Subject: "; later words follow a
	// single folding space.
	limit := encodedWidth + 1 - len("Subject: ") - len(wordPrefix) - len(wordSuffix)
	for len(subject) > 0 {
		_, size := utf8.DecodeRuneInString(subject)
		chunk := qEncode(subject[:size])
		if payload.Len() > 0 && payload.Len()+len(chunk) > limit {
			words = append(words, wordPrefix+payload.String()+wordSuffix)
			payload.Reset()
			limit = encodedWidth - len(wordPrefix) - len(wordSuffix)
		}
		payload.WriteString(chunk)
		subject = subject[size:]
	}
	return append(words, wordPrefix+payload.String()+wordSuffix)
}

// qEncode applies the Q encoding to one character. Only letters, digits and a
// few symbols that RFC 2047 section 5 allows in a phrase are left as they are.
func qEncode(char string) string {
	const hexDigits = "0123456789ABCDEF"
	if len(char) == 1 {
		c := char[0]
		switch {
		case c == ' ':
			return "_"
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("!*+-/", c) >= 0:
			return char
		}
	}
	var out strings.Builder
	for i := 0; i < len(char); i++ {
		out.WriteByte('=')
		out.WriteByte(hexDigits[char[i]>>4])
		out.WriteByte(hexDigits[char[i]&0x0f])
	}
	return out.String()
}
