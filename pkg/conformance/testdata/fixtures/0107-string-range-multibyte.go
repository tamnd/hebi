package main

import "fmt"

func main() {
	for i, r := range "héllo" {
		fmt.Println(i, r)
	}
}
