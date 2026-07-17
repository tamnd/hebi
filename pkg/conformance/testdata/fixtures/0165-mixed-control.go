package main

import "fmt"

func main() {
	total := 0
	for i := 1; i <= 5; i++ {
		if i == 3 {
			continue
		}
		total = total + i*i
	}
	if total > 20 {
		fmt.Println("large", total)
	} else {
		fmt.Println("small", total)
	}
}
