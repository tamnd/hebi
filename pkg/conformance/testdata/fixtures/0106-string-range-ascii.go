package main

import "fmt"

func main() {
	for i, r := range "abc" {
		fmt.Println(i, r)
	}
}
