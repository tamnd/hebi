package main

import "fmt"

func main() {
	a := [2][2]int{{1, 2}, {3, 4}}
	a[1][0] = 30
	fmt.Println(a[1][0], a[0][0])
}
