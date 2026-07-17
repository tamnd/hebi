package main

import "fmt"

func banner() {
	fmt.Println("report")
}

func main() {
	banner()
	total := 0
	i := 1
	for i <= 3 {
		total = total + i*i
		i = i + 1
	}
	if total > 10 {
		fmt.Println("large", total)
	} else {
		fmt.Println("small", total)
	}
}
