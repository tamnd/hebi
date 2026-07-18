package main

import "fmt"

func main() {
	s := []int{1, 2, 3}
	t := s[:]
	q := &s[0]
	*q = 100
	fmt.Println(t[0])
}
