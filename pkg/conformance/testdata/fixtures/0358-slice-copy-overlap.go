package main

import "fmt"

func main() {
	s := []int{1, 2, 3, 4, 5}
	copy(s[1:], s[0:4])
	fmt.Println(s)
}
