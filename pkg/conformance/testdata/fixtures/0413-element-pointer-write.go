package main

import "fmt"

func main() {
	a := [3]int{1, 2, 3}
	q := &a[1]
	*q = 20
	fmt.Println(a)
}
