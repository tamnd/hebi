package main

import "fmt"

func main() {
	m := map[string]int{"a": 1}
	v, ok := m["a"]
	if ok {
		fmt.Println("found", v)
	}
	_, no := m["z"]
	if !no {
		fmt.Println("missing")
	}
}
