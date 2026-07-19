package main

import (
	"fmt"
	"sync"
)

func main() {
	var m sync.Map
	m.Store("a", 1)
	m.Store("b", 2)
	m.Store("c", 3)
	count := 0
	sum := 0
	m.Range(func(k, v any) bool {
		count++
		sum += v.(int)
		return true
	})
	fmt.Println(count, sum)
}
