package shortener

import "testing"

func TestEncodeKnownVectors(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0"},
		{9, "9"},
		{10, "A"},
		{61, "z"},
		{62, "10"},
		{63, "11"},
		{3843, "zz"},  // 62*62 - 1
		{3844, "100"}, // 62*62
		{1000000, "4C92"},
	}
	for _, c := range cases {
		if got := Encode(c.in); got != c.want {
			t.Errorf("Encode(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	// Sample values across the whole uint64 range, including boundaries.
	values := []uint64{0, 1, 61, 62, 1000000, 1<<32 - 1, 1 << 32, 1<<63 + 123, 1<<64 - 1}
	for _, v := range values {
		got, err := Decode(Encode(v))
		if err != nil {
			t.Fatalf("Decode(Encode(%d)) unexpected error: %v", v, err)
		}
		if got != v {
			t.Errorf("round trip for %d = %d", v, got)
		}
	}
}

func TestDecodeRejectsInvalid(t *testing.T) {
	for _, s := range []string{"", "abc$", "hello world", "+/"} {
		if _, err := Decode(s); err == nil {
			t.Errorf("Decode(%q) expected error, got nil", s)
		}
	}
}

// TestEncodeIsCollisionFree asserts the core guarantee the design relies on:
// encoding a range of distinct sequence values yields distinct codes.
func TestEncodeIsCollisionFree(t *testing.T) {
	seen := make(map[string]uint64)
	const start, n = uint64(1_000_000), 100_000
	for v := start; v < start+n; v++ {
		code := Encode(v)
		if prev, dup := seen[code]; dup {
			t.Fatalf("collision: Encode(%d) and Encode(%d) both = %q", prev, v, code)
		}
		seen[code] = v
	}
}

func TestEncodeIsURLSafe(t *testing.T) {
	for v := uint64(0); v < 5000; v++ {
		for _, r := range Encode(v) {
			ok := (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
			if !ok {
				t.Fatalf("Encode(%d) produced non-URL-safe rune %q", v, r)
			}
		}
	}
}
