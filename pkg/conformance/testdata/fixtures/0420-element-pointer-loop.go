package main

import "fmt"

func main() {
	a := [4]int{1, 2, 3, 4}
	for i := 0; i < len(a); i++ {
		p := &a[i]
		*p = *p * 10
	}
	fmt.Println(a)
}
