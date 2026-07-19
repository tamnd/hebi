package main

import (
	"errors"
	"fmt"
)

func main() {
	base := errors.New("not found")
	wrapped := fmt.Errorf("open config: %w", base)
	fmt.Println(wrapped)
}
