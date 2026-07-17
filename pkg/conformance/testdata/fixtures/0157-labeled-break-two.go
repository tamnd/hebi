package main

import "fmt"

func main() {
Outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				if k == 1 {
					break Outer
				}
				fmt.Println(i, j, k)
			}
		}
	}
}
