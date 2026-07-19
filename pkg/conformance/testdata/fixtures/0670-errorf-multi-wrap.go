package main

import (
	"errors"
	"fmt"
)

func main() {
	a := errors.New("a")
	b := errors.New("b")
	e := fmt.Errorf("both %w and %w", a, b)
	fmt.Println(e)
	fmt.Println(errors.Is(e, a), errors.Is(e, b))
}
