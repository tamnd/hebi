package main

import "fmt"

func main() {
	total := 0
	for i := range "hello" {
		total = total + i
	}
	fmt.Println(total)
}
