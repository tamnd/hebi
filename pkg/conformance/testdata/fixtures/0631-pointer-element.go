package main

import "fmt"

func main() {
	xs := []int{10, 20, 30}
	p := &xs[1]
	*p = 200
	fmt.Println(xs)
}
