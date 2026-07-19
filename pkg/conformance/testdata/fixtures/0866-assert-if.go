package main

import "fmt"

func describe(x interface{}) string {
	s, ok := x.(string)
	if ok {
		return "str:" + s
	}
	return "other"
}

func main() {
	fmt.Println(describe("hi"))
	fmt.Println(describe(3))
}
