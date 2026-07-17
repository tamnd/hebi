package main

import "fmt"

func main() {
	for i := 0; i < 6; i++ {
		if i == 2 {
			continue
		}
		fmt.Println(i)
	}
}
