package main

import "fmt"

func main() {
Outer:
	for i := range 3 {
		for _, r := range "abc" {
			if r == 98 {
				break Outer
			}
			fmt.Println(i, r)
		}
	}
}
