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
	err := errors.New("plain")
	var target *MyErr
	ok := errors.As(err, &target)
	if !ok {
		fmt.Println("no match")
	}
}
