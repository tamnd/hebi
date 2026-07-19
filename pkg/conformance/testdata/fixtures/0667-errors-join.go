package main

import (
	"errors"
	"fmt"
)

func main() {
	a := errors.New("one")
	b := errors.New("two")
	j := errors.Join(a, b)
	fmt.Println(j)
}
