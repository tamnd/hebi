package main

import "fmt"

func main() {
	var x interface{} = 7
	n, ok := x.(int)
	fmt.Println(n, ok)
}
