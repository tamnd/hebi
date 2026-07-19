package main

import "fmt"

type Point struct {
	X int
}

func (p Point) Bumped() Point {
	p.X = p.X + 1
	return p
}

func main() {
	p := Point{X: 1}
	q := p.Bumped()
	fmt.Println(p.X, q.X)
}
