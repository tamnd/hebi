package main

import "fmt"

func bump(p *int) {
	*p = *p + 1
}

func main() {
	n := 10
	bump(&n)
	bump(&n)
	fmt.Println(n)
}
