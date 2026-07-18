package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	var p Point
	fmt.Println(p.X, p.Y)
}
