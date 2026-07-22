package shortener

import "errors"

// alphabet is the URL-safe base62 character set. It deliberately excludes the
// standard-base64 characters '+' and '/' so codes never need percent-encoding.
const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

const base = uint64(len(alphabet)) // 62

// decodeTable maps a byte to its base62 value, or -1 if it is not a valid
// base62 character. Built once at init for O(1) lookups during Decode.
var decodeTable [256]int

func init() {
	for i := range decodeTable {
		decodeTable[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		decodeTable[alphabet[i]] = i
	}
}

// ErrInvalidCode is returned by Decode when the input contains a character that
// is not part of the base62 alphabet.
var ErrInvalidCode = errors.New("invalid base62 code")

// Encode converts a non-negative integer into its base62 string representation.
//
// Encode is a bijection between the non-negative integers and base62 strings:
// distinct inputs always produce distinct outputs. This is the property that
// guarantees auto-generated short codes never collide — each code is derived
// from a value drawn from a strictly increasing sequence, and no two sequence
// values map to the same string.
func Encode(n uint64) string {
	if n == 0 {
		return string(alphabet[0])
	}
	// A uint64 needs at most 11 base62 digits (62^11 > 2^64).
	buf := make([]byte, 0, 11)
	for n > 0 {
		buf = append(buf, alphabet[n%base])
		n /= base
	}
	// Digits were produced least-significant first; reverse them.
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// Decode converts a base62 string back into its integer value. It is the
// inverse of Encode. Returns ErrInvalidCode for empty or malformed input.
func Decode(s string) (uint64, error) {
	if s == "" {
		return 0, ErrInvalidCode
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		d := decodeTable[s[i]]
		if d < 0 {
			return 0, ErrInvalidCode
		}
		n = n*base + uint64(d)
	}
	return n, nil
}
