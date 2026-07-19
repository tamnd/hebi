package main

import "fmt"

func compute() (result int) {
	defer func() {
		result *= 2
	}()
	result = 21
	return
}

func main() {
	fmt.Println(compute())
}
