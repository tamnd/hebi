package main

import (
	"fmt"
	"sync/atomic"
)

func main() {
	var n atomic.Int32
	n.Add(5)
	n.Add(-2)
	fmt.Println(n.Load())
	n.Store(100)
	fmt.Println(n.Add(1))
}
