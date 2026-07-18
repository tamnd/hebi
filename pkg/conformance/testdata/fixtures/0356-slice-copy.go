package main

import "fmt"

func main() {
	dst := make([]int, 3)
	src := []int{7, 8, 9}
	n := copy(dst, src)
	fmt.Println(n, dst)
}
