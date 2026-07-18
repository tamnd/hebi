package main

import "fmt"

func main() {
	s := []int{}
	for i := 0; i < 5; i++ {
		s = append(s, i*i)
	}
	fmt.Println(s, len(s))
}
