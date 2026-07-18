package main

import "fmt"

type Inner struct {
	V int
}

type Outer struct {
	In Inner
	N  int
}

func main() {
	o := Outer{In: Inner{5}, N: 7}
	fmt.Println(o.In.V, o.N)
}
