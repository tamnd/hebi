package main

import "fmt"

type A struct {
	V int
}

type B struct {
	A
}

type C struct {
	B
}

func main() {
	c := C{B{A{5}}}
	c.V = 8
	fmt.Println(c.V, c.B.A.V)
}
