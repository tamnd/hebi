package main

import "fmt"

type Point struct{ X, Y int }

func main() {
	fmt.Printf("%v %+v\n", []int{1, 2, 3}, []Point{{1, 2}, {3, 4}})
	fmt.Printf("%v\n", map[string]int{"b": 2, "a": 1, "c": 3})
}
