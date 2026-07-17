package main

import "fmt"

func main() {
	for i, r := range "a☃b" {
		fmt.Println(i, r)
	}
}
