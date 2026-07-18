package main

import "fmt"

func main() {
	m := map[int]int{1: 1, 2: 2, 3: 3}
	sum := 0
	for k := range m {
		sum = sum + k
	}
	fmt.Println(sum)
}
