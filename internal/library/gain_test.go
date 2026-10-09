package library

import (
	"database/sql"
	"testing"
)

func ptr(v float64) *float64 { return &v }

func TestGainDB(t *testing.T) {
	f := func(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
	cases := []struct {
		l, p sql.NullFloat64
		want *float64
	}{
		{sql.NullFloat64{}, f(-3), nil},      // unmeasured
		{f(-9), f(-0.5), ptr(-5)},            // loud: -14-(-9) = -5; peak -0.5-5 = -5.5 ≤ -1
		{f(-20), f(-3), ptr(0)},              // quiet: never boosted
		{f(-14.5), f(0.8), ptr(-1.8)},        // peak cap: -1-0.8 = -1.8 < 0
		{f(-10), sql.NullFloat64{}, ptr(-4)}, // no peak reading: loudness alone
		{f(-14), f(-1), ptr(0)},              // exactly on target: 0, not -0
	}
	for i, c := range cases {
		got := GainDB(c.l, c.p)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("case %d: GainDB(%v, %v) = %v, want %v", i, c.l, c.p, deref(got), deref(c.want))
		}
		if got != nil && *got == 0 && 1/(*got) < 0 {
			t.Errorf("case %d: negative zero", i)
		}
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
