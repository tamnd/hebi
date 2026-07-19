package main

import (
	"fmt"
	"sync"
)

func main() {
	var mu sync.RWMutex
	balance := 0
	var wg sync.WaitGroup
	for range 25 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			balance += 4
			mu.Unlock()
		}()
	}
	wg.Wait()
	mu.RLock()
	fmt.Println(balance)
	mu.RUnlock()
}
