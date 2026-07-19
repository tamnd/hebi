package main

import "fmt"

func main() {
	a := 1
	outer := func() int {
		b := 2
		inner := func() int {
			return a + b
		}
		return inner()
	}
	fmt.Println(outer())
}
