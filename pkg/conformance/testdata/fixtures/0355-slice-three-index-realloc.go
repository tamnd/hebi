package main

import "fmt"

func main() {
	s := []int{1, 2, 3, 4, 5}
	t := s[0:2:2]
	t = append(t, 99)
	fmt.Println(s[2], t[2])
}
