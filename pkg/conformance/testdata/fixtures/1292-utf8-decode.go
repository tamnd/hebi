package main

import (
	"fmt"
	"unicode/utf8"
)

func main() {
	r, size := utf8.DecodeRuneInString("界x")
	fmt.Println(r, size)
	lr, ls := utf8.DecodeLastRuneInString("x界")
	fmt.Println(lr, ls)
	er, es := utf8.DecodeRuneInString("")
	fmt.Println(er, es)
	br, bs := utf8.DecodeRune([]byte{0xff})
	fmt.Println(br, bs)
	dr, ds := utf8.DecodeRune([]byte("é"))
	fmt.Println(dr, ds)
}
