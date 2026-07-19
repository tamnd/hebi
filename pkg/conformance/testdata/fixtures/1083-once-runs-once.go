package main

import (
	"fmt"
	"sync"
)

func main() {
	var once sync.Once
	var wg sync.WaitGroup
	var mu sync.Mutex
	calls := 0
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			once.Do(func() {
				mu.Lock()
				calls++
				mu.Unlock()
			})
		}()
	}
	wg.Wait()
	fmt.Println(calls)
}
