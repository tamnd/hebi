package main

import "fmt"

func main() {
	x := 9
	switch x {
	case 1:
		fmt.Println("one")
	default:
		fmt.Println("other")
	case 2:
		fmt.Println("two")
	}
}
