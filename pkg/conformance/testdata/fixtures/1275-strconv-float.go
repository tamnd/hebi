package main

import (
	"fmt"
	"strconv"
)

func main() {
	f, _ := strconv.ParseFloat("2.71828", 64)
	fmt.Println(f)
	fmt.Println(strconv.FormatFloat(3.14159, 'f', 3, 64))
	fmt.Println(strconv.FormatFloat(98765.4321, 'e', 2, 64))
	fmt.Println(strconv.FormatFloat(0.5, 'g', -1, 64))
	_, err := strconv.ParseFloat("bad", 64)
	fmt.Println(err)
}
