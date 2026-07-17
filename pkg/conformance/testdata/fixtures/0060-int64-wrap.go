package main

import "fmt"

func main() {
	var x int64 = 9223372036854775800
	x = x + 10
	fmt.Println(x)
}
