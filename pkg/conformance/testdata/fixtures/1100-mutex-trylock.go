package main

import (
	"fmt"
	"sync"
)

func main() {
	var mu sync.Mutex
	mu.Lock()
	locked := mu.TryLock()
	fmt.Println(locked)
	mu.Unlock()
	got := mu.TryLock()
	fmt.Println(got)
	if got {
		mu.Unlock()
	}
}
