package main

import (
	"errors"
	"fmt"
)

type MyErr struct {
	Code int
}

func (e *MyErr) Error() string {
	return "myerr"
}

func main() {
	base := &MyErr{Code: 9}
	wrapped := fmt.Errorf("wrap: %w", base)
	var target *MyErr
	if errors.As(wrapped, &target) {
		fmt.Println("found", target.Code)
	}
}
