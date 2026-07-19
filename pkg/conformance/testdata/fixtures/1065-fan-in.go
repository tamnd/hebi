package main

import (
	"fmt"
	"sync"
)

func main() {
	out := make(chan int)
	var wg sync.WaitGroup
	for start := 0; start < 3; start++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				out <- s*10 + i
			}
		}(start)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	sum := 0
	for v := range out {
		sum += v
	}
	fmt.Println(sum)
}
