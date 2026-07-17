package main

import "fmt"

func main() {
	i := 1
	product := 1
	for i <= 4 {
		product = product * i
		i = i + 1
	}
	fmt.Println(product)
}
