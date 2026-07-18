package main

import "fmt"

func main() {
	s := []int{1, 2, 3, 4}
	t := s[1:3]
	t[0] = 99
	fmt.Println(s[1], t[0])
}
