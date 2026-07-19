package main

import "fmt"

func pair() (int, int) {
	return 6, 7
}

func main() {
	a, b := pair()
	fmt.Println(a * b)
}
