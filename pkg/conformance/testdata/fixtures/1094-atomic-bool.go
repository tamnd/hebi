package main

import (
	"fmt"
	"sync/atomic"
)

func main() {
	var flag atomic.Bool
	fmt.Println(flag.Load())
	flag.Store(true)
	fmt.Println(flag.Load())
	swapped := flag.CompareAndSwap(true, false)
	fmt.Println(swapped, flag.Load())
}
