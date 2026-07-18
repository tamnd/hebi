package main

import "fmt"

func main() {
	var s []int
	s = append(s, 1)
	s = append(s, 2)
	fmt.Println(s, len(s))
}
