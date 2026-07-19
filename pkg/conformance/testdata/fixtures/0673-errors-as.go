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
	var err error = &MyErr{Code: 7}
	var target *MyErr
	if errors.As(err, &target) {
		fmt.Println("matched", target.Code)
	}
}
