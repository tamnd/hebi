package main

import "fmt"

func kind(x interface{}) string {
	switch x.(type) {
	case int:
		return "int"
	case string:
		return "string"
	default:
		return "other"
	}
}

func main() {
	fmt.Println(kind(1), kind("a"), kind(1.5))
}
