package main

import "fmt"

type Inner struct {
	V int
}

type Outer struct {
	In Inner
}

func read(o Outer) int {
	o.In.V = 999
	return o.In.V
}

func main() {
	o := Outer{Inner{5}}
	got := read(o)
	fmt.Println(got, o.In.V)
}
