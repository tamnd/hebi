package main

import "fmt"

func main() {
	if 2 < 1 {
		fmt.Println("unreachable")
	}
	fmt.Println("done")
}
