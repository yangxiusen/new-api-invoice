package money

import (
	"math"
	"testing"
)

func TestExactCents(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int64
	}{{"1.005", 101}, {"0.005", 1}, {"12.994", 1299}, {"92233720368547758.07", math.MaxInt64}} {
		got, err := Cents(c.raw)
		if err != nil || got != c.want {
			t.Fatalf("%s: got %d %v", c.raw, got, err)
		}
	}
	for _, s := range []string{"NaN", "Infinity", "-1", "0", "0.004", "92233720368547758.08", "1e999999", ""} {
		if _, err := Cents(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	if _, err := Add(math.MaxInt64, 1); err == nil {
		t.Fatal("overflow accepted")
	}
}
