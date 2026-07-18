package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	s := []Point{}
	p := Point{1, 2}
	s = append(s, p)
	p.X = 99
	fmt.Println(s[0].X)
}
