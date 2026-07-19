package main

import (
	"fmt"
	"sync"
)

func main() {
	mu := sync.Mutex{}
	cond := sync.NewCond(&mu)
	start := false
	var wg sync.WaitGroup
	var cmu sync.Mutex
	count := 0
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			for !start {
				cond.Wait()
			}
			mu.Unlock()
			cmu.Lock()
			count++
			cmu.Unlock()
		}()
	}
	mu.Lock()
	start = true
	cond.Broadcast()
	mu.Unlock()
	wg.Wait()
	fmt.Println(count)
}
