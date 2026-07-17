package main

import "fmt"

func main() {
	var x uint16 = 0xABCD
	fmt.Println(x<<8, (x<<8)>>8)
}
