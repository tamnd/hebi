package main

import (
	"fmt"
	"strconv"
)

func main() {
	a, _ := strconv.ParseInt("7f", 16, 64)
	b, _ := strconv.ParseInt("0b1010", 0, 64)
	c, _ := strconv.ParseUint("777", 8, 64)
	fmt.Println(a, b, c)
	_, err := strconv.ParseInt("abcz", 16, 64)
	fmt.Println(err)
}
