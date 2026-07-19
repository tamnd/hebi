package main

import (
	"errors"
	"fmt"
)

func main() {
	a := errors.New("first")
	b := errors.New("second")
	j := errors.Join(a, b)
	fmt.Println(errors.Is(j, a), errors.Is(j, b))
}
