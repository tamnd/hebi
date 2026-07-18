package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	a := [2]Point{{1, 2}, {3, 4}}
	fmt.Println(a[0].X, a[1].Y)
}
