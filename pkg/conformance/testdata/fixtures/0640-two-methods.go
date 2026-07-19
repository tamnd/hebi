package main

import "fmt"

type Rect struct {
	W, H int
}

func (r Rect) Area() int {
	return r.W * r.H
}

func (r Rect) Perimeter() int {
	return 2 * (r.W + r.H)
}

func main() {
	r := Rect{W: 3, H: 4}
	fmt.Println(r.Area(), r.Perimeter())
}
