package main

import "fmt"

func main() {
	x := 1
	for i := 0; i < 5; i++ {
		x *= 2
	}
	fmt.Println(x)
}
