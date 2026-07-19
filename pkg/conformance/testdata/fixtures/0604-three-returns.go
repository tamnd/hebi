package main

import "fmt"

func stats(a, b, c int) (int, int, int) {
	return a + b + c, a * b * c, c - a
}

func main() {
	s, p, d := stats(2, 3, 4)
	fmt.Println(s, p, d)
}
