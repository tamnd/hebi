package main

import "fmt"

func main() {
Outer:
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if j == 1 {
				continue Outer
			}
			if i == 3 {
				break Outer
			}
			fmt.Println(i, j)
		}
	}
}
