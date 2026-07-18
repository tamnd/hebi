package main

import "fmt"

func main() {
	a := [3]int{1, 2, 3}
	b := a
	b[0] = 100
	fmt.Println(a[0], b[0])
}
