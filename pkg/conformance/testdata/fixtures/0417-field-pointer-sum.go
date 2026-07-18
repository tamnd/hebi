package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	p := Point{3, 4}
	px := &p.X
	py := &p.Y
	fmt.Println(*px + *py)
}
