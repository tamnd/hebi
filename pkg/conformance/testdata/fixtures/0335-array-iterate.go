package main

import "fmt"

func main() {
	a := [4]int{2, 4, 6, 8}
	sum := 0
	for i := 0; i < len(a); i++ {
		sum = sum + a[i]
	}
	fmt.Println(sum)
}
