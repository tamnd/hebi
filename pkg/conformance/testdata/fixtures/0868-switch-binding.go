package main

import "fmt"

func main() {
	var x interface{} = 10
	switch v := x.(type) {
	case int:
		fmt.Println(v * 2)
	case string:
		fmt.Println(v + v)
	}
}
