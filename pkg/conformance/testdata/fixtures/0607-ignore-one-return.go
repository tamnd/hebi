package main

import "fmt"

func labeled() (string, int) {
	return "n", 42
}

func main() {
	_, n := labeled()
	fmt.Println(n)
}
