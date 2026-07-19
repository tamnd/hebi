package main

import "fmt"

func main() {
	xs := []any{1, "two", 3.0, true}
	for i := 0; i < len(xs); i++ {
		fmt.Println(xs[i])
	}
}
