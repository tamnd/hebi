package main

import "fmt"

func main() {
	s := [][]int{{1, 2}, {3, 4, 5}}
	fmt.Println(s[0], s[1], len(s[1]))
}
