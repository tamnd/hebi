package main

import "fmt"

type Inner struct {
	V int
}

type Outer struct {
	In Inner
}

func main() {
	a := Outer{Inner{5}}
	b := a
	b.In.V = 50
	fmt.Println(a.In.V, b.In.V)
}
