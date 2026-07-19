package main

import "fmt"

func main() {
	ch := make(chan int)
	select {
	case v := <-ch:
		fmt.Println("recv", v)
	default:
		fmt.Println("no value ready")
	}
}
