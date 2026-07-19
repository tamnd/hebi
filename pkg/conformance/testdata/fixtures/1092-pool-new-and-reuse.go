package main

import (
	"fmt"
	"sync"
)

func main() {
	news := 0
	pool := sync.Pool{
		New: func() any {
			news++
			return 100
		},
	}
	a := pool.Get().(int)
	fmt.Println(a)
	pool.Put(7)
	b := pool.Get().(int)
	fmt.Println(b)
	c := pool.Get().(int)
	fmt.Println(c)
	fmt.Println("news", news)
}
