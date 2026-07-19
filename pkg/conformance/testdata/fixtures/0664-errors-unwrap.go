package main

import (
	"errors"
	"fmt"
)

func main() {
	base := errors.New("root")
	wrapped := fmt.Errorf("layer: %w", base)
	fmt.Println(errors.Unwrap(wrapped))
	fmt.Println(errors.Unwrap(base))
}
