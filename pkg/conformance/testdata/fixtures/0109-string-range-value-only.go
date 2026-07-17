package main

import "fmt"

func main() {
	sum := 0
	for _, r := range "abc" {
		sum = sum + int(r)
	}
	fmt.Println(sum)
}
