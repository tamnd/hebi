package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

func main() {
	items := make(chan int, 100)
	var produced atomic.Int64
	var consumed atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				items <- i
				produced.Add(1)
			}
		}()
	}
	go func() {
		wg.Wait()
		close(items)
	}()
	var cwg sync.WaitGroup
	for range 4 {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for range items {
				consumed.Add(1)
			}
		}()
	}
	cwg.Wait()
	fmt.Println(produced.Load(), consumed.Load())
}
