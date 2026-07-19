package main

import "fmt"

func main() {
	a, b := 2, 3
	defer fmt.Println("sum was", a+b)
	a, b = 10, 20
	fmt.Println("now", a+b)
}
