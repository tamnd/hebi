package main

import "fmt"

func main() {
	ch := make(chan int)
	go func() { ch <- 99 }()
	fmt.Println(<-ch)
}
