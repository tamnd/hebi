package main

import "fmt"

type Point struct {
	X int
	Y int
}

func main() {
	m := map[string]Point{"a": {1, 2}}
	fmt.Println(m["a"].X, m["a"].Y)
}
