package main

import "fmt"

func main() {
	s := []bool{true, false, true}
	s = append(s, false)
	fmt.Println(s, len(s))
}
