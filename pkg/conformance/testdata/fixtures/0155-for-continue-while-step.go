package main

import "fmt"

func main() {
	i := 0
	for i < 5 {
		i = i + 1
		if i == 3 {
			continue
		}
		fmt.Println(i)
	}
}
