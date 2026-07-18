package main

import "fmt"

type Point struct {
	X int
	Y int
}

func shift(p Point, d int) Point {
	p.X = p.X + d
	return p
}

func main() {
	a := Point{1, 1}
	b := shift(a, 5)
	fmt.Println(a.X, b.X)
}
