package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	p := Point{Y: 5}
	fmt.Println(p.X, p.Y)
}
