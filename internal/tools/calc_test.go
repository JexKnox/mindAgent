package tools

import (
	"math"
	"testing"
)

func TestEvalArith(t *testing.T) {
	cases := []struct {
		expr string
		want float64
	}{
		{"1+2", 3},
		{"(1+2)*3.5", 10.5},
		{"100/8", 12.5},
		{"-5+3", -2},
		{"2*(3+4)*-1", -14},
		{"1/0.5", 2},
	}
	for _, c := range cases {
		got, err := evalArith(c.expr)
		if err != nil {
			t.Errorf("evalArith(%q) 报错: %v", c.expr, err)
			continue
		}
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("evalArith(%q) = %v, 期望 %v", c.expr, got, c.want)
		}
	}
}

func TestEvalArithErrors(t *testing.T) {
	for _, expr := range []string{"", "1/0", "1+", "foo", "os.Exit(1)"} {
		if _, err := evalArith(expr); err == nil {
			t.Errorf("evalArith(%q) 期望报错，实际成功", expr)
		}
	}
}
