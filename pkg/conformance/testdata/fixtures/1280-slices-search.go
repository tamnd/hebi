package main

import (
	"fmt"
	"slices"
)

func main() {
	a := []string{"go", "rust", "zig"}
	fmt.Println(slices.Contains(a, "rust"), slices.Index(a, "zig"))
	fmt.Println(slices.IndexFunc(a, func(s string) bool { return len(s) == 3 }))
	fmt.Println(slices.ContainsFunc(a, func(s string) bool { return s == "c" }))
}
