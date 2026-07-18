package main

import "fmt"

type Key struct {
	A int
	B int
}

func main() {
	m := map[Key]int{{1, 2}: 10, {3, 4}: 20}
	fmt.Println(m[Key{1, 2}], m[Key{3, 4}])
}
