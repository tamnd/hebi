package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	m := map[Point]int{}
	m[Point{1, 2}] = 5
	fmt.Println(m[Point{1, 2}], m[Point{3, 4}])
}
