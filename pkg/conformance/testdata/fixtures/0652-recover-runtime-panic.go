package main

import "fmt"

func lookup(i int) (v int) {
	defer func() {
		recover()
		v = -1
	}()
	s := []int{10, 20, 30}
	v = s[i]
	return v
}

func main() {
	fmt.Println(lookup(1), lookup(9))
}
