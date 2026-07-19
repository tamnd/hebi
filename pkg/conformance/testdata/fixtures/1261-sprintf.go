package main

import "fmt"

func main() {
	s := fmt.Sprintf("%d-%s-%v-%06.2f", 9, "z", true, 3.5)
	fmt.Println(s)
	fmt.Println(len(s))
}
