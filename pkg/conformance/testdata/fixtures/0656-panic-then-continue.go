package main

import "fmt"

func attempt(bad bool) (ok bool) {
	defer func() {
		recover()
	}()
	if bad {
		panic("nope")
	}
	ok = true
	return
}

func main() {
	fmt.Println(attempt(false), attempt(true))
}
