package main

import "fmt"

func inc() (n int) {
	defer func() {
		n = n + 1
	}()
	return 5
}

func main() {
	fmt.Println(inc())
}
