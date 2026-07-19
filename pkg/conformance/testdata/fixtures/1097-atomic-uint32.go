package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

func main() {
	var counter atomic.Uint32
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counter.Add(3)
		}()
	}
	wg.Wait()
	fmt.Println(counter.Load())
}
