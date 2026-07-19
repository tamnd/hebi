package main

import (
	"errors"
	"fmt"
)

func main() {
	base := errors.New("sentinel")
	wrapped := fmt.Errorf("wrap: %w", base)
	if errors.Is(wrapped, base) {
		fmt.Println("is base")
	}
	other := errors.New("other")
	if !errors.Is(wrapped, other) {
		fmt.Println("not other")
	}
}
