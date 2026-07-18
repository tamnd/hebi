package main

import "fmt"

func main() {
	m := map[string][]int{}
	m["a"] = append(m["a"], 1)
	m["a"] = append(m["a"], 2)
	fmt.Println(m["a"], len(m["a"]))
}
