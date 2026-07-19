package main

import (
	"fmt"
	"strings"
)

func main() {
	s := "hello world hello"
	fmt.Println(strings.Contains(s, "world"), strings.Contains(s, "nope"))
	fmt.Println(strings.HasPrefix(s, "hello"), strings.HasSuffix(s, "hello"))
	fmt.Println(strings.Index(s, "hello"), strings.LastIndex(s, "hello"))
	fmt.Println(strings.IndexByte(s, 'w'), strings.ContainsRune(s, 'x'))
	fmt.Println(strings.Count(s, "l"), strings.Count("ab", ""))
}
