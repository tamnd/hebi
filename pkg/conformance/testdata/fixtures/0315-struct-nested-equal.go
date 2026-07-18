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
	a := Outer{Inner{1}, 2}
	b := Outer{Inner{1}, 2}
	c := Outer{Inner{9}, 2}
	fmt.Println(a == b, a == c)
}
