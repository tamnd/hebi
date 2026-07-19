package main

import "fmt"

type Any interface{ M() string }

type A struct{}

func (A) M() string { return "a" }

func main() {
	var x Any = A{}
	a := x.(A)
	fmt.Println(a.M())
}
