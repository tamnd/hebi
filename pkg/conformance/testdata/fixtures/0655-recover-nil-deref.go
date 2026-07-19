package main

import "fmt"

func deref(p *int) (v int) {
	defer func() {
		recover()
		v = -1
	}()
	v = *p
	return v
}

func main() {
	n := 7
	var np *int
	fmt.Println(deref(&n), deref(np))
}
