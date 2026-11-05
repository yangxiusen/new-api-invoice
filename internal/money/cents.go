package money

import (
	"errors"
	"math"
	"strings"

	"github.com/shopspring/decimal"
)

var ErrAmount = errors.New("invalid or overflowing amount")

// Cents converts a bounded exact numeric string, rounding each order separately.
func Cents(raw string) (int64, error) {
	if len(raw) == 0 || len(raw) > 64 || strings.ContainsAny(raw, "eE") {
		return 0, ErrAmount
	}
	d, err := decimal.NewFromString(raw)
	if err != nil || !d.IsPositive() {
		return 0, ErrAmount
	}
	c := d.Mul(decimal.NewFromInt(100)).Round(0)
	if c.LessThan(decimal.NewFromInt(1)) || c.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, ErrAmount
	}
	return c.IntPart(), nil
}

func Add(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, ErrAmount
	}
	return a + b, nil
}
