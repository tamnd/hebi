package main

import "fmt"

type Point struct{ X, Y int }
type Outer struct {
	Name string
	P    Point
}

func main() {
	o := Outer{"hi", Point{3, 4}}
	fmt.Printf("%v\n%+v\n%#v\n", o, o, o)
}
