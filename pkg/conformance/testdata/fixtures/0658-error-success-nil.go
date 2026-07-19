package main

import (
	"errors"
	"fmt"
)

func check(n int) error {
	if n < 0 {
		return errors.New("negative")
	}
	return nil
}

func main() {
	fmt.Println(check(5))
	fmt.Println(check(-1))
}
