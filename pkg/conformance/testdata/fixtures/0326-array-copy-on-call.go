package main

import "fmt"

func zero(a [3]int) {
	a[0] = 0
}

func main() {
	a := [3]int{7, 8, 9}
	zero(a)
	fmt.Println(a[0])
}
