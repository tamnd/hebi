package main

import "fmt"

func main() {
	total := 0
	add := func(x int) {
		total += x
	}
	get := func() int {
		return total
	}
	add(3)
	add(4)
	fmt.Println(get())
}
