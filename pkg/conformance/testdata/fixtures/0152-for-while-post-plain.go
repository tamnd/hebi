package main

import "fmt"

func main() {
	sum := 0
	for i := 0; i < 4; i = i + 1 {
		sum = sum + i
	}
	fmt.Println(sum)
}
