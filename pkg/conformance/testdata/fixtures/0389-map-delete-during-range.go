package main

import "fmt"

func main() {
	m := map[int]int{1: 1, 2: 2, 3: 3}
	for k := range m {
		if k == 2 {
			delete(m, 2)
		}
	}
	fmt.Println(len(m))
}
