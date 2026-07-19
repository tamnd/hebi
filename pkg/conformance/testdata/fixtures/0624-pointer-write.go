package main

import "fmt"

func main() {
	x := 1
	p := &x
	*p = 99
	fmt.Println(x)
}
