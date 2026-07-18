package main

import "fmt"

func main() {
	s := []int{1, 2, 3, 4}
	t := s[:2]
	fmt.Println(t, len(t))
}
