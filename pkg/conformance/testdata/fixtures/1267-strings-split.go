package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.Split("a,b,c", ","))
	fmt.Println(strings.Split("go", ""))
	fmt.Println(strings.SplitN("a,b,c,d", ",", 2))
	fmt.Println(strings.SplitN("a,b,c,d", ",", -1))
	fmt.Println(strings.Fields("  one two   three "))
	fmt.Println(strings.Join([]string{"a", "b", "c"}, "+"))
}
