package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	results := make([]int, 8)
	for i := 0; i < len(results); i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = idx * 10
		}(i)
	}
	wg.Wait()
	fmt.Println(results)
}
