package main

import "fmt"

func main() {
	s := []int{1, 2, 3}
	t := s[:]
	t[0] = 99
	fmt.Println(s[0], t[0])
}
