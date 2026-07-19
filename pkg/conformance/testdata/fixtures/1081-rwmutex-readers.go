package main

import (
	"fmt"
	"sync"
)

func main() {
	var mu sync.RWMutex
	data := 100
	var wg sync.WaitGroup
	var rmu sync.Mutex
	sum := 0
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.RLock()
			v := data
			mu.RUnlock()
			rmu.Lock()
			sum += v
			rmu.Unlock()
		}()
	}
	wg.Wait()
	fmt.Println(sum)
}
