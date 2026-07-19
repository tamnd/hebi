package main

import (
	"errors"
	"fmt"
)

func main() {
	var a, b error
	fmt.Println(errors.Join(a, b))
}
