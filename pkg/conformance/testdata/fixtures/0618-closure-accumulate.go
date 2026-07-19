package main

import "fmt"

func accumulator() func(int) int {
	sum := 0
	return func(x int) int {
		sum += x
		return sum
	}
}

func main() {
	acc := accumulator()
	fmt.Println(acc(1), acc(2), acc(3))
}
