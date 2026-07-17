package main

import "fmt"

func main() {
	sum := 0.0
	for i := 0; i < 5; i++ {
		sum = sum + 0.5
	}
	fmt.Println(sum)
}
