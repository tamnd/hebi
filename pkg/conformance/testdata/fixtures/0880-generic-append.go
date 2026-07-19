package main

import "fmt"

func Push[T any](xs []T, v T) []T { return append(xs, v) }

func main() {
	fmt.Println(Push([]int{1, 2}, 3))
}
