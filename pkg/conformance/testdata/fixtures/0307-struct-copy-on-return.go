package main

import "fmt"

type Point struct {
	X int
	Y int
}

func origin() Point {
	return Point{0, 0}
}

func main() {
	p := origin()
	p.X = 3
	q := origin()
	fmt.Println(p.X, q.X)
}
