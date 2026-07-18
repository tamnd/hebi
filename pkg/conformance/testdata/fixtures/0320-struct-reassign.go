package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	p := Point{1, 2}
	p = Point{3, 4}
	fmt.Println(p.X, p.Y)
}
