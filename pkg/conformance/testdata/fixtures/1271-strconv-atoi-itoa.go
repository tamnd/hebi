package main

import (
	"fmt"
	"strconv"
)

func main() {
	n, err := strconv.Atoi("2024")
	fmt.Println(n, err)
	fmt.Println(strconv.Itoa(-512))
	_, err = strconv.Atoi("12x")
	fmt.Println(err)
	_, err = strconv.Atoi("")
	fmt.Println(err)
}
