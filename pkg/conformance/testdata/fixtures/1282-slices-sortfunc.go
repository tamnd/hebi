package main

import (
	"fmt"
	"slices"
)

type city struct {
	Name string
	Pop  int
}

func main() {
	cities := []city{{"A", 300}, {"B", 100}, {"C", 200}}
	slices.SortFunc(cities, func(x, y city) int { return x.Pop - y.Pop })
	fmt.Println(cities)
}
