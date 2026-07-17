package main

import "fmt"

func main() {
	a := true
	b := false
	c := true
	fmt.Println(a || b || c, a && b && c)
}
