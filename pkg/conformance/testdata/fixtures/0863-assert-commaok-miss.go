package main

import "fmt"

func main() {
	var x interface{} = "s"
	n, ok := x.(int)
	fmt.Println(n, ok)
}
