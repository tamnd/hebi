package main

import (
	"fmt"
	"slices"
)

func main() {
	a := []int{1, 1, 1, 2, 3, 3, 5}
	a = slices.Compact(a)
	fmt.Println(a)
	i, ok := slices.BinarySearch(a, 3)
	fmt.Println(i, ok)
	i, ok = slices.BinarySearch(a, 4)
	fmt.Println(i, ok)
}
