package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	a := [2]Point{{1, 2}, {3, 4}}
	b := a
	b[0].X = 99
	fmt.Println(a[0].X, b[0].X)
}
