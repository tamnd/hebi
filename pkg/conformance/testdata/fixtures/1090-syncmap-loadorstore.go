package main

import (
	"fmt"
	"sync"
)

func main() {
	var m sync.Map
	actual, loaded := m.LoadOrStore("k", 1)
	fmt.Println(actual, loaded)
	actual2, loaded2 := m.LoadOrStore("k", 2)
	fmt.Println(actual2, loaded2)
}
