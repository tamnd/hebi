package main

import "fmt"

func reorder(a, b, c int) (int, int, int) {
	return c, a, b
}

func main() {
	x, y, z := reorder(1, 2, 3)
	fmt.Println(x, y, z)
}
