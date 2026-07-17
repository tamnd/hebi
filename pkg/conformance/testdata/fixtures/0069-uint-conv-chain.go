package main

import "fmt"

func main() {
	var a int32 = 70000
	var b uint16 = uint16(a)
	var c int8 = int8(b)
	fmt.Println(b, c)
}
