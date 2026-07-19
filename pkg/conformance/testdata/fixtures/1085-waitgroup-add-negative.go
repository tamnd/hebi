package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	wg.Add(5)
	for range 5 {
		go func() {
			mu.Lock()
			done++
			mu.Unlock()
			wg.Done()
		}()
	}
	wg.Wait()
	fmt.Println(done)
}
