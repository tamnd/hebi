package main

import "fmt"

func main() {
	a := true
	b := false
	if a && b {
		fmt.Println("both")
	} else {
		fmt.Println("not both")
	}
}
