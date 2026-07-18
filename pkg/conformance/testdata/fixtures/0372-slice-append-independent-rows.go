package main

import "fmt"

func main() {
	a := make([]int, 0, 2)
	b := make([]int, 0, 2)
	a = append(a, 1)
	b = append(b, 2)
	fmt.Println(a[0], b[0])
}
