package main

import "fmt"

func main() {
	n := 10
	add := func(x int) int {
		return n + x
	}
	fmt.Println(add(5))
}
