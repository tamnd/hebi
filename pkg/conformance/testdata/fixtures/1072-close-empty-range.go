package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	close(ch)
	count := 0
	for range ch {
		count++
	}
	fmt.Println(count)
}
