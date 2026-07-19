package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 1; i <= 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mu.Lock()
			total += n
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	fmt.Println(total)
}
