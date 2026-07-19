package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

func main() {
	var counter atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				counter.Add(1)
			}
		}()
	}
	wg.Wait()
	fmt.Println(counter.Load())
}
