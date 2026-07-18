package main

import "fmt"

func main() {
	m := map[string]int{}
	m["a"] = 1
	m["a"] = 5
	fmt.Println(m["a"], len(m))
}
