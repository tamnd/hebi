package main

import (
	"fmt"
	"sort"
)

func main() {
	a := []int{9, 3, 7, 1, 5}
	sort.Ints(a)
	fmt.Println(a, sort.IntsAreSorted(a))
	s := []string{"delta", "alpha", "charlie", "bravo"}
	sort.Strings(s)
	fmt.Println(s)
	f := []float64{2.2, 0.1, 1.5}
	sort.Float64s(f)
	fmt.Println(f, sort.Float64sAreSorted(f))
}
