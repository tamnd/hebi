package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var inner sync.WaitGroup
			for range 4 {
				inner.Add(1)
				go func() {
					defer inner.Done()
					mu.Lock()
					total++
					mu.Unlock()
				}()
			}
			inner.Wait()
		}()
	}
	wg.Wait()
	fmt.Println(total)
}
