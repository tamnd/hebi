package main

import "fmt"

func minmax(a, b int) (int, int) {
	if a < b {
		return a, b
	}
	return b, a
}

func main() {
	lo, hi := minmax(17, 5)
	fmt.Println(lo, hi)
}
