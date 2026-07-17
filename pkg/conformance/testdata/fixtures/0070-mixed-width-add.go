package main

import "fmt"

func main() {
	var a int16 = 300
	var b int16 = 300
	var c int8 = int8(a) + int8(b)
	fmt.Println(c)
}
