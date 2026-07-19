package main

import "fmt"

type T interface{ M() }

func main() {
	var t T
	fmt.Println(t == nil)
}
