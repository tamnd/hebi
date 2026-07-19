package main

import "fmt"

func at(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func main() {
	n := 8
	var np *int
	fmt.Println(at(&n), at(np))
}
