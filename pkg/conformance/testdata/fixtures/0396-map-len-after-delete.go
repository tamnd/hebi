package main

import "fmt"

func main() {
	m := map[int]int{1: 1, 2: 2}
	delete(m, 1)
	delete(m, 9)
	fmt.Println(len(m))
}
