package main

import "fmt"

func adder(base int) func(int) int {
	return func(x int) int {
		return base + x
	}
}

func main() {
	add10 := adder(10)
	fmt.Println(add10(1), add10(2))
}
