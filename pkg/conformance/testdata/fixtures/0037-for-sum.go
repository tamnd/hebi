package main

import "fmt"

func main() {
	i := 1
	sum := 0
	for i <= 5 {
		sum = sum + i
		i = i + 1
	}
	fmt.Println(sum)
}
