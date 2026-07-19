package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println(math.IsNaN(math.NaN()), math.IsInf(math.Inf(1), 1))
	fmt.Println(math.Sqrt(-4), math.Mod(1, 0))
	fmt.Println(math.Max(math.Inf(1), 100), math.Min(math.Inf(-1), -100))
}
