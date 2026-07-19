package main

import "fmt"

func classify(n int) (string, int) {
	if n < 0 {
		return "neg", -n
	}
	if n == 0 {
		return "zero", 0
	}
	return "pos", n
}

func main() {
	a, b := classify(-4)
	c, d := classify(0)
	e, f := classify(7)
	fmt.Println(a, b, c, d, e, f)
}
