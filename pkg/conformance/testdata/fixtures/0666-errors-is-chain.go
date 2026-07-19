package main

import (
	"errors"
	"fmt"
)

func main() {
	a := errors.New("a")
	b := fmt.Errorf("b: %w", a)
	c := fmt.Errorf("c: %w", b)
	fmt.Println(c)
	if errors.Is(c, a) {
		fmt.Println("reaches a")
	}
}
