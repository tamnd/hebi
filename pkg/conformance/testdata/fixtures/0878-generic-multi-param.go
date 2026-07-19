package main

import "fmt"

func Pair[K comparable, V any](k K, v V) {
	fmt.Println(k, v)
}

func main() {
	Pair(1, "x")
	Pair("k", 2)
}
