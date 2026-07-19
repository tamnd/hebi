package main

import (
	"fmt"
	"sort"
)

func main() {
	nums := []int{4, 2, 6, 1}
	less := func(i, j int) bool { return nums[i] < nums[j] }
	fmt.Println(sort.SliceIsSorted(nums, less))
	sort.Slice(nums, less)
	fmt.Println(nums, sort.SliceIsSorted(nums, less))
}
