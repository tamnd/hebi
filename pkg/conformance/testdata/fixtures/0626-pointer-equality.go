package main

import "fmt"

func main() {
	x := 5
	p := &x
	q := &x
	y := 5
	r := &y
	fmt.Println(p == q, p == r)
}
