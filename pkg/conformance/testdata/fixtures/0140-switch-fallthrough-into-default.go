package main

import "fmt"

func main() {
	switch 2 {
	case 2:
		fmt.Println("two")
		fallthrough
	default:
		fmt.Println("default")
	}
}
