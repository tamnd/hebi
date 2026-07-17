package main

import "fmt"

func main() {
	var x uint32 = 1
	fmt.Println(x<<0, x<<1, x<<16, x<<31)
}
