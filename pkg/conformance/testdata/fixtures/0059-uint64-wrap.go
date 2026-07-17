package main

import "fmt"

func main() {
	var x uint64 = 18446744073709551610
	x = x + 10
	fmt.Println(x)
}
