package main

import "fmt"

func main() {
	x := 1
	defer fmt.Println("deferred x:", x)
	x = 100
	fmt.Println("current x:", x)
}
