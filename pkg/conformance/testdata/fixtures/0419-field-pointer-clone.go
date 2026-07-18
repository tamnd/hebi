package main

import "fmt"

type Inner struct {
	V int
}

type Outer struct {
	In Inner
}

func main() {
	o := Outer{Inner{5}}
	p := &o.In
	src := Inner{9}
	*p = src
	src.V = 100
	fmt.Println(o.In.V)
}
