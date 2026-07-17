package main

import "fmt"

func main() {
	s := ""
	fmt.Println(len(s))
	for i, r := range s {
		fmt.Println(i, r)
	}
	fmt.Println("done")
}
