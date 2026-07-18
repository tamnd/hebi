package main

import "fmt"

func main() {
	m := map[string]int{"a": 1}
	m["a"] = 2
	fmt.Println(m["a"])
}
