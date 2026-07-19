package main

import (
	"fmt"
	"sync"
)

func main() {
	jobs := make(chan int, 20)
	var wg sync.WaitGroup
	var mu sync.Mutex
	sum := 0
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				mu.Lock()
				sum += j
				mu.Unlock()
			}
		}()
	}
	for i := 1; i <= 20; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	fmt.Println(sum)
}
