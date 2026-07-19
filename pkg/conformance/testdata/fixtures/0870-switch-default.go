package main

import "fmt"

func main() {
	var x interface{} = true
	switch x.(type) {
	case int:
		fmt.Println("int")
	default:
		fmt.Println("default")
	}
}
