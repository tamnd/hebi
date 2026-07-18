package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	p := Point{1, 2}
	px := &p.X
	*px = 9
	fmt.Println(p.X)
}
