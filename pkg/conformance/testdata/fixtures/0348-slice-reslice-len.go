package main

import "fmt"

func main() {
	s := []int{1, 2, 3, 4, 5}
	t := s[1:4]
	fmt.Println(len(t), t[0], t[2])
}
