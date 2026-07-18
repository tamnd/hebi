package main

import "fmt"

func main() {
	s := []int{1, 2, 3}
	q := &s[2]
	*q = 30
	fmt.Println(s)
}
