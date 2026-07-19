package main

import "fmt"

func tag(n int) (string, int) {
	return "v", n * 2
}

func main() {
	s, v := tag(21)
	fmt.Println(s, v)
}
