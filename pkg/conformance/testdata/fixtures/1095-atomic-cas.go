package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

func main() {
	var value atomic.Int64
	value.Store(0)
	var wg sync.WaitGroup
	var success atomic.Int64
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				old := value.Load()
				if value.CompareAndSwap(old, old+1) {
					success.Add(1)
					return
				}
			}
		}()
	}
	wg.Wait()
	fmt.Println(value.Load(), success.Load())
}
