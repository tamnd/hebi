package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	s := []Point{{1, 2}, {3, 4}}
	s[0].X = 10
	fmt.Println(s[0].X, s[1].Y)
}
