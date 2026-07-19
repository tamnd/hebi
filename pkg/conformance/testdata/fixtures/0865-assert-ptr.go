package main

import "fmt"

type C struct{ v int }

func main() {
	var x interface{} = &C{9}
	c, ok := x.(*C)
	fmt.Println(c.v, ok)
}
