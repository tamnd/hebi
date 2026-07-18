package main

import "fmt"

func first(s []int) int {
	s[0] = 42
	return s[0]
}

func main() {
	s := []int{1, 2, 3}
	got := first(s)
	fmt.Println(got, s[0])
}
