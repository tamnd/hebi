package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	s := []Point{{1, 2}, {3, 4}}
	fmt.Println(s[0].X, s[1].Y)
}
