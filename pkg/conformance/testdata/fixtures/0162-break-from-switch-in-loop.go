package main

import "fmt"

func main() {
	for i := 0; i < 4; i++ {
		switch i {
		case 2:
			fmt.Println("two")
		default:
			fmt.Println(i)
		}
	}
}
