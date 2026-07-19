package main

import "fmt"

func positive(n int) (ok bool) {
	ok = n > 0
	return
}

func main() {
	fmt.Println(positive(4), positive(-5))
}
