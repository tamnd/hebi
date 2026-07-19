package main

import "fmt"

func main() {
	r := func(a, b int) int {
		return a * b
	}(6, 7)
	fmt.Println(r)
}
