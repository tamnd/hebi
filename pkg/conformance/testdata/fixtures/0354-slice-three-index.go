package main

import "fmt"

func main() {
	s := []int{1, 2, 3, 4, 5}
	t := s[1:3:4]
	fmt.Println(len(t), cap(t))
}
