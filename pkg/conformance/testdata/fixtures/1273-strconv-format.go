package main

import (
	"fmt"
	"strconv"
)

func main() {
	fmt.Println(strconv.FormatInt(3735928559, 16))
	fmt.Println(strconv.FormatInt(-42, 2), strconv.FormatUint(64, 8))
	fmt.Println(strconv.FormatBool(true), strconv.FormatBool(false))
}
