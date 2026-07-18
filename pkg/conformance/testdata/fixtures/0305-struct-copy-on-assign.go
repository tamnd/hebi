package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	a := Point{1, 2}
	b := a
	b.X = 99
	fmt.Println(a.X, b.X)
}
