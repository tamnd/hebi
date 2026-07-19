package main

import "fmt"

func Id[T any](x T) T { return x }

func main() {
	fmt.Println(Id[int](5), Id[string]("s"))
}
