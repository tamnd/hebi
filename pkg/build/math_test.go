package build

import (
	"testing"
)

// TestMathFuncs checks the math package lowering against go run. It covers the
// exact functions, the rounding, sign, remainder, comparison, and
// classification surface, along with the constant fold that lets math.Pi and
// the integer limits emit their value, held byte for byte against Go's output.
func TestMathFuncs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"rounding and sign",
			`package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println(math.Abs(-3.5), math.Ceil(2.1), math.Floor(2.9), math.Trunc(-2.7))
	fmt.Println(math.Round(2.5), math.Round(-2.5), math.Round(0.4))
	fmt.Println(math.Copysign(3, -1), math.Signbit(math.Copysign(0, -1)), math.Signbit(1))
}
`,
		},
		{
			"sqrt mod dim max min",
			`package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println(math.Sqrt(16), math.Sqrt(2))
	fmt.Println(math.Mod(10, 3), math.Mod(-7, 3))
	fmt.Println(math.Dim(5, 2), math.Dim(2, 5))
	fmt.Println(math.Max(3, 7), math.Min(3, 7))
}
`,
		},
		{
			"nan and inf classification",
			`package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println(math.IsNaN(math.NaN()), math.IsNaN(1.0))
	fmt.Println(math.IsInf(math.Inf(1), 1), math.IsInf(math.Inf(-1), -1), math.IsInf(1, 0))
	fmt.Println(math.Sqrt(-1), math.Max(math.NaN(), 1))
}
`,
		},
		{
			"constant fold",
			`package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println(math.Pi)
	fmt.Println(math.MaxInt32, math.MinInt32)
	fmt.Println(math.MaxInt8)
	x := math.Pi * 2
	fmt.Println(x)
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}
