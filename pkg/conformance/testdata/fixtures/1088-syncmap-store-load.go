package main

import (
	"fmt"
	"sync"
)

func main() {
	var m sync.Map
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			m.Store(k, k*k)
		}(i)
	}
	wg.Wait()
	sum := 0
	for i := range 10 {
		v, ok := m.Load(i)
		if ok {
			sum += v.(int)
		}
	}
	fmt.Println(sum)
}
