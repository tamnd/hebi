package main

import "fmt"

func size(x interface{}) string {
	switch x.(type) {
	case int, int64:
		return "integer"
	case float32, float64:
		return "float"
	default:
		return "?"
	}
}

func main() {
	fmt.Println(size(1), size(1.0), size("x"))
}
