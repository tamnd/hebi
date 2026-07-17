package main

import "fmt"

func main() {
	x := 2
	if x == 1 {
		fmt.Println("one")
	} else if x == 2 {
		fmt.Println("two")
	} else if x == 3 {
		fmt.Println("three")
	} else {
		fmt.Println("many")
	}
}
