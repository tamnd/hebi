package main

import "fmt"

func swap(a, b *int) {
	t := *a
	*a = *b
	*b = t
}

func main() {
	x := 1
	y := 2
	swap(&x, &y)
	fmt.Println(x, y)
}
