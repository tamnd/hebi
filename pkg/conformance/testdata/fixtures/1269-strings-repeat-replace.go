package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.Repeat("ab", 4))
	fmt.Println(strings.Replace("aaaaa", "a", "b", 3))
	fmt.Println(strings.ReplaceAll("one.two.three", ".", "/"))
	fmt.Println(strings.EqualFold("HeLLo", "hello"), strings.EqualFold("a", "b"))
}
