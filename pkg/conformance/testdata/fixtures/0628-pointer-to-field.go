package main

import "fmt"

type Box struct {
	V int
}

func main() {
	b := Box{V: 3}
	p := &b.V
	*p = 30
	fmt.Println(b.V)
}
