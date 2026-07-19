package main

import "fmt"

func main() {
	ch := make(chan int, 1)
	ch <- 7
	close(ch)
	a, ok1 := <-ch
	b, ok2 := <-ch
	fmt.Println(a, ok1)
	fmt.Println(b, ok2)
}
