package main

import (
	"errors"
	"fmt"
)

func load(bad bool) error {
	if bad {
		return errors.New("disk error")
	}
	return nil
}

func process(bad bool) error {
	err := load(bad)
	if err != nil {
		return fmt.Errorf("process: %w", err)
	}
	return nil
}

func main() {
	fmt.Println(process(true))
	fmt.Println(process(false))
}
