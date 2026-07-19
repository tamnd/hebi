package main

import "fmt"

func main() {
	for i := 0; i < 3; i++ {
		defer fmt.Println("defer", i)
	}
	fmt.Println("body done")
}
