package main

import "fmt"

func main() {
	var x any = 42
	n := x.(int)
	fmt.Println(n + 1)
}
