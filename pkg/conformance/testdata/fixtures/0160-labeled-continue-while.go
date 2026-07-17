package main

import "fmt"

func main() {
	i := 0
Outer:
	for i < 3 {
		i = i + 1
		for j := 0; j < 3; j++ {
			if j == 1 {
				continue Outer
			}
			fmt.Println(i, j)
		}
	}
}
