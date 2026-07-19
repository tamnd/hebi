package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println(math.Abs(-9.25), math.Ceil(4.01), math.Floor(4.99), math.Trunc(-4.6))
	fmt.Println(math.Round(1.5), math.Round(2.5), math.Round(-1.5), math.Round(0.49))
}
