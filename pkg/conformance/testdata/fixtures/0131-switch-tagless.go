package main

import "fmt"

func main() {
	x := 15
	switch {
	case x < 10:
		fmt.Println("small")
	case x < 20:
		fmt.Println("medium")
	default:
		fmt.Println("large")
	}
}
