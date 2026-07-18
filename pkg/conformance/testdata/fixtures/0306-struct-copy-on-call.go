package main

import "fmt"

type Point struct {
	X int
	Y int
}

func bump(p Point) {
	p.X = 100
}

func main() {
	a := Point{1, 2}
	bump(a)
	fmt.Println(a.X)
}
