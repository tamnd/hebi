package main

import "fmt"

func main() {
	x := 1
	switch x {
	case 1:
		fmt.Println("one")
		fallthrough
	case 2:
		fmt.Println("two")
	case 3:
		fmt.Println("three")
	}
}
