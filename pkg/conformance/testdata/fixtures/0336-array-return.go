package main

import "fmt"

func make3() [3]int {
	return [3]int{1, 2, 3}
}

func main() {
	a := make3()
	a[0] = 9
	b := make3()
	fmt.Println(a[0], b[0])
}
