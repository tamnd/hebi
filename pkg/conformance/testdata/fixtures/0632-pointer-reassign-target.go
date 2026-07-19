package main

import "fmt"

func main() {
	x := 1
	y := 2
	p := &x
	fmt.Println(*p)
	p = &y
	fmt.Println(*p)
}
