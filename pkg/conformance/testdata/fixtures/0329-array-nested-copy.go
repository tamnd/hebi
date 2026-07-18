package main

import "fmt"

func main() {
	a := [2][2]int{{1, 2}, {3, 4}}
	b := a
	b[0][0] = 99
	fmt.Println(a[0][0], b[0][0])
}
