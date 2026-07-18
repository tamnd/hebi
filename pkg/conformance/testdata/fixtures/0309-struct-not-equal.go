package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	a := Point{1, 2}
	b := Point{1, 3}
	fmt.Println(a != b)
}
