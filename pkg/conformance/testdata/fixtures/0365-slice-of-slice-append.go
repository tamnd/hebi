package main

import "fmt"

func main() {
	rows := [][]int{}
	rows = append(rows, []int{1, 2})
	rows = append(rows, []int{3})
	fmt.Println(rows[0], rows[1], len(rows))
}
