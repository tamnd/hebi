package main

import "fmt"

func main() {
	m := map[int]int{}
	keys := []int{0, 1, 0, 1, 0}
	for i := 0; i < len(keys); i++ {
		k := keys[i]
		m[k] = m[k] + i
	}
	fmt.Println(m[0], m[1])
}
