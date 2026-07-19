package main

import (
	"fmt"
	"sync"
)

func main() {
	pool := sync.Pool{
		New: func() any { return 0 },
	}
	v := pool.Get().(int)
	fmt.Println(v)
	pool.Put(42)
	got := pool.Get().(int)
	fmt.Println(got)
}
