package main

import "fmt"

func main() {
	i := 5
	for i < 3 {
		fmt.Println(i)
		i = i + 1
	}
	fmt.Println("after")
}
