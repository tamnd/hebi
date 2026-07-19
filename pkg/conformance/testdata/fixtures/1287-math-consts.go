package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println(math.Pi, math.E)
	fmt.Println(math.MaxInt64, math.MinInt64)
	fmt.Println(math.MaxInt32, math.MaxUint16)
	area := math.Pi * 5 * 5
	fmt.Println(area)
}
