package main

import "fmt"

func main() {
	s := make([]int, 2, 5)
	fmt.Println(len(s), cap(s))
}
