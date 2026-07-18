package main

import "fmt"

func main() {
	s := []int{1}
	s = append(s, 2, 3, 4)
	fmt.Println(s, len(s))
}
