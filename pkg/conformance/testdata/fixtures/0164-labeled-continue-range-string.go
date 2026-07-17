package main

import "fmt"

func main() {
Outer:
	for _, r := range "abc" {
		for j := 0; j < 3; j++ {
			if j == 1 {
				continue Outer
			}
			fmt.Println(r, j)
		}
	}
}
