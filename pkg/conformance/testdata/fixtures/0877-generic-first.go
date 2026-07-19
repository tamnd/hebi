package main

import "fmt"

func First[T any](xs []T) T { return xs[0] }

func main() {
	fmt.Println(First([]int{10, 20}), First([]string{"a", "b"}))
}
