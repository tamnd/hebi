package main

import "fmt"

func main() {
	counts := map[string]int{}
	words := []string{"a", "b", "a", "c", "a"}
	for i := 0; i < len(words); i++ {
		counts[words[i]] = counts[words[i]] + 1
	}
	fmt.Println(counts["a"], counts["b"], counts["c"])
}
