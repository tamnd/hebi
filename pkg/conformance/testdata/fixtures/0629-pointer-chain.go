package main

import "fmt"

func main() {
	x := 7
	p := &x
	*p = *p * *p
	fmt.Println(x)
}
