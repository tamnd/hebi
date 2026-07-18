package main

import "fmt"

type Point struct {
	X int
	Y int
}

func same(a Point, b Point) bool {
	return a == b
}

func main() {
	p := Point{3, 4}
	q := p
	fmt.Println(same(p, q))
}
