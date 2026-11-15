// Package license is the desktop app's side of Eggify licensing: serial
// numbers, the install key, the signed license token, the license server
// client and the manager that keeps the token renewed.
//
// The wire contract (serial format, token, signed requests, API) is
// docs/licensing-protocol.md; the decisions behind it are
// docs/adr/013-licensing.md. Follow the protocol document exactly: the
// license server (Rust) and the relay are built from it too.
package license

import (
	"errors"
	"strings"
)

// Alphabet is Crockford base32 (value = index).
const Alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// SerialLen is the number of characters in a normalised serial: 19 random
// characters and 1 check character.
const SerialLen = 20

// ErrInvalidSerial is returned for input that is not a valid serial number.
var ErrInvalidSerial = errors.New("That isn't a valid serial number. Check it for typos: it looks like EGG-XXXXX-XXXXX-XXXXX-XXXXX.")

func alphabetValue(c byte) int { return strings.IndexByte(Alphabet, c) }

// CheckChar returns the check character for a 19-character body:
// ALPHABET[(Σ i·value(c_i)) mod 31], i 1-based. body must be valid alphabet
// characters.
func CheckChar(body string) byte {
	sum := 0
	for i := 0; i < len(body); i++ {
		sum += (i + 1) * alphabetValue(body[i])
	}
	return Alphabet[sum%31]
}

// NormalizeSerial turns user input into the normalised 20 characters:
// upper-case; spaces, "-" and "_" dropped; a leading "EGG" dropped unless
// exactly 20 characters are left; O→0,
// I/L→1. The result must be 20 alphabet characters with a correct check
// character, otherwise ErrInvalidSerial.
func NormalizeSerial(input string) (string, error) {
	s := strings.ToUpper(input)
	s = strings.NewReplacer(" ", "", "-", "", "_", "", "\t", "", "\n", "", "\r", "").Replace(s)
	if len(s) > SerialLen {
		s = strings.TrimPrefix(s, "EGG")
	}
	s = strings.NewReplacer("O", "0", "I", "1", "L", "1").Replace(s)
	if len(s) != SerialLen {
		return "", ErrInvalidSerial
	}
	for i := 0; i < len(s); i++ {
		if alphabetValue(s[i]) < 0 {
			return "", ErrInvalidSerial
		}
	}
	if CheckChar(s[:SerialLen-1]) != s[SerialLen-1] {
		return "", ErrInvalidSerial
	}
	return s, nil
}

// FormatSerial formats normalised characters as EGG-XXXXX-XXXXX-XXXXX-XXXXX.
func FormatSerial(norm string) string {
	var b strings.Builder
	b.WriteString("EGG")
	for i := 0; i < len(norm); i += 5 {
		end := min(i+5, len(norm))
		b.WriteByte('-')
		b.WriteString(norm[i:end])
	}
	return b.String()
}

// SerialFromBody appends the check character to a 19-character body and
// formats the serial (used by tests and tooling).
func SerialFromBody(body string) string {
	return FormatSerial(body + string(CheckChar(body)))
}

// SerialHint is the display form the server also uses: the last group only,
// e.g. "EGG-…-V5N0T".
func SerialHint(norm string) string {
	if len(norm) < 5 {
		return ""
	}
	return "EGG-…-" + norm[len(norm)-5:]
}
