package main

import (
	"fmt"
	"strconv"
)

func main() {
	b, err := strconv.ParseBool("false")
	fmt.Println(b, err)
	_, err = strconv.ParseBool("maybe")
	fmt.Println(err)
	fmt.Println(strconv.Quote("a\tb\nc"))
}
