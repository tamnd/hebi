package main

import "fmt"

func main() {
	s := make([]int, 2, 5)
	s[0] = 1
	s[1] = 2
	t := append(s, 3)
	t[0] = 99
	fmt.Println(s[0], t[0], len(t))
}
