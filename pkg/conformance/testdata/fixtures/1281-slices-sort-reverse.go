package main

import (
	"fmt"
	"slices"
)

func main() {
	a := []int{7, 3, 9, 1, 5}
	slices.Sort(a)
	fmt.Println(a, slices.Min(a), slices.Max(a))
	slices.Reverse(a)
	fmt.Println(a)
	b := slices.Clone(a)
	fmt.Println(slices.Equal(a, b))
}
