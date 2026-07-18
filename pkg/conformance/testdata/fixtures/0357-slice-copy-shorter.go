package main

import "fmt"

func main() {
	dst := make([]int, 2)
	src := []int{1, 2, 3, 4}
	n := copy(dst, src)
	fmt.Println(n, dst)
}
