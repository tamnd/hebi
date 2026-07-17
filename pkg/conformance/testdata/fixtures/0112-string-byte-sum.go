package main

import "fmt"

func main() {
	s := "hello"
	sum := 0
	for i := 0; i < len(s); i++ {
		sum = sum + int(s[i])
	}
	fmt.Println(sum)
}
