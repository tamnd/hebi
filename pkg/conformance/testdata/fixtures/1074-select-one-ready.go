package main

import "fmt"

func main() {
	a := make(chan int, 1)
	b := make(chan int, 1)
	b <- 5
	select {
	case v := <-a:
		fmt.Println("a", v)
	case v := <-b:
		fmt.Println("b", v)
	}
}
