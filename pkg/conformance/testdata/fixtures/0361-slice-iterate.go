package main

import "fmt"

func main() {
	s := []int{5, 10, 15}
	sum := 0
	for i := 0; i < len(s); i++ {
		sum = sum + s[i]
	}
	fmt.Println(sum)
}
