package main

import "fmt"

func main() {
	switch 1 {
	case 1:
		fmt.Println("a")
		fallthrough
	case 2:
		fmt.Println("b")
		fallthrough
	case 3:
		fmt.Println("c")
	}
}
