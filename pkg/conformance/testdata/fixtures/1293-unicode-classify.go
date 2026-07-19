package main

import (
	"fmt"
	"unicode"
)

func main() {
	rs := []rune{'A', 'z', '5', ' ', '\t', '\n', 0x00A0, '_', '.', ',', '!', '?', ';', 0x4e16, 0x03B1, 0x0391, 0x00B5, 0x2160, 0x00BD, 0x0660, 0x2028, 0x3000, 0x1F600, 0x00, 0x1F, 0x7F, 0x9F}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		fmt.Println(r,
			unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsNumber(r),
			unicode.IsUpper(r), unicode.IsLower(r), unicode.IsSpace(r),
			unicode.IsControl(r), unicode.IsPunct(r))
	}
}
