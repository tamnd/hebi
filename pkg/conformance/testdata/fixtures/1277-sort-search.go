package main

import (
	"fmt"
	"sort"
)

func main() {
	a := []int{2, 4, 6, 8, 10}
	fmt.Println(sort.SearchInts(a, 6), sort.SearchInts(a, 7), sort.SearchInts(a, 11))
	n := sort.Search(50, func(i int) bool { return i >= 17 })
	fmt.Println(n)
}
