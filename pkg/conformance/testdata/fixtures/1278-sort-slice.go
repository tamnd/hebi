package main

import (
	"fmt"
	"sort"
)

type item struct {
	Name  string
	Score int
}

func main() {
	items := []item{{"x", 3}, {"y", 1}, {"z", 2}}
	sort.Slice(items, func(i, j int) bool { return items[i].Score < items[j].Score })
	fmt.Println(items)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name > items[j].Name })
	fmt.Println(items)
}
