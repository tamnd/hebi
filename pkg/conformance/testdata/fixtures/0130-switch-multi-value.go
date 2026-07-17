package main

import "fmt"

func main() {
	x := 3
	switch x {
	case 1, 2, 3:
		fmt.Println("low")
	case 4, 5, 6:
		fmt.Println("high")
	}
}
