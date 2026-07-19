package main

import "fmt"

func main() {
	xs := []int{1, 2, 3}
	scale := func(k int) {
		for i := 0; i < 3; i++ {
			xs[i] = xs[i] * k
		}
	}
	scale(10)
	fmt.Println(xs)
}
