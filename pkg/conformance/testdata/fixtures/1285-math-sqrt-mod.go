package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println(math.Sqrt(144), math.Sqrt(3))
	fmt.Println(math.Mod(17, 5), math.Mod(-17, 5), math.Mod(17, -5))
	fmt.Println(math.Max(2.5, 2.6), math.Min(2.5, 2.6), math.Dim(8, 3))
}
