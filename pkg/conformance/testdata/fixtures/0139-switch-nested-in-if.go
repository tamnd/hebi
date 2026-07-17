package main

import "fmt"

func main() {
	x := 1
	if x > 0 {
		switch x {
		case 1:
			fmt.Println("one positive")
		}
	}
}
