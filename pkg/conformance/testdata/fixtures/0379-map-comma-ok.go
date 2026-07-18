package main

import "fmt"

func main() {
	m := map[string]int{"a": 1}
	v, ok := m["a"]
	x, no := m["z"]
	fmt.Println(v, ok, x, no)
}
