package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	got := make([]int, 5)
	for i := 0; i < len(got); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = i * i
		}()
	}
	wg.Wait()
	fmt.Println(got)
}
