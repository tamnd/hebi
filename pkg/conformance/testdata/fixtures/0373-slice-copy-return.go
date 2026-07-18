package main

import "fmt"

func clone(s []int) []int {
	out := make([]int, len(s))
	copy(out, s)
	return out
}

func main() {
	s := []int{1, 2, 3}
	c := clone(s)
	c[0] = 99
	fmt.Println(s[0], c[0])
}
