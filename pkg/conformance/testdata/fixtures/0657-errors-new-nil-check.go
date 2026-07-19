package main

import (
	"errors"
	"fmt"
)

func find(ok bool) (int, error) {
	if !ok {
		return 0, errors.New("not found")
	}
	return 42, nil
}

func main() {
	v, err := find(false)
	if err != nil {
		fmt.Println("error:", err)
	}
	fmt.Println(v)
}
