package main

import (
	"fmt"
	"sync"
)

func main() {
	mu := sync.Mutex{}
	cond := sync.NewCond(&mu)
	ready := false
	go func() {
		mu.Lock()
		ready = true
		cond.Signal()
		mu.Unlock()
	}()
	mu.Lock()
	for !ready {
		cond.Wait()
	}
	mu.Unlock()
	fmt.Println("ready")
}
