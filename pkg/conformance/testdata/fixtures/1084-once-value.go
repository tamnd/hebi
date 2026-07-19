package main

import (
	"fmt"
	"sync"
)

func main() {
	var once sync.Once
	config := ""
	load := func() string {
		once.Do(func() { config = "loaded" })
		return config
	}
	fmt.Println(load(), load(), load())
}
