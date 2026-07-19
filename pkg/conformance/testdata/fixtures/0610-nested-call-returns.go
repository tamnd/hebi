package main

import "fmt"

func inc(n int) int {
	return n + 1
}

func twice(n int) (int, int) {
	return inc(n), inc(inc(n))
}

func main() {
	a, b := twice(10)
	fmt.Println(a, b)
}
