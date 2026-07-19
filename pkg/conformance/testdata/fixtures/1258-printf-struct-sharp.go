package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	p := Point{1, 2}
	fmt.Printf("%#v %T\n", p, p)
}
