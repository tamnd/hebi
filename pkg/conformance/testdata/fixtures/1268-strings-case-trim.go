package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.ToUpper("MixedCase"), strings.ToLower("MixedCase"))
	fmt.Printf("[%s]\n", strings.TrimSpace("  \t hi \n "))
	fmt.Printf("[%s] [%s] [%s]\n", strings.Trim("**hi**", "*"), strings.TrimLeft("**hi", "*"), strings.TrimRight("hi**", "*"))
	fmt.Println(strings.TrimPrefix("prefix-body", "prefix-"), strings.TrimSuffix("body-suffix", "-suffix"))
	fmt.Println(strings.TrimPrefix("body", "zzz"))
}
