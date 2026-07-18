package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	a := [3]Point{1: {5, 6}}
	a[0].X = 1
	fmt.Println(a[0].X, a[1].X, a[2].X)
}
