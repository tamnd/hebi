package main

import (
	"fmt"
	"sync/atomic"
)

func main() {
	var v atomic.Int64
	v.Store(10)
	old := v.Swap(20)
	fmt.Println(old, v.Load())
}
