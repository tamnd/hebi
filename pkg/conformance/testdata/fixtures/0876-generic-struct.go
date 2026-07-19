package main

import "fmt"

type Box[T any] struct{ V T }

func (b Box[T]) Get() T { return b.V }

func main() {
	fmt.Println(Box[int]{7}.Get(), Box[string]{"x"}.Get())
}
