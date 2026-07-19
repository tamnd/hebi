package main

import "fmt"

func f() {
	defer fmt.Println("first")
	defer func() {
		recover()
		fmt.Println("second")
	}()
	panic("x")
}

func main() {
	f()
	fmt.Println("done")
}
