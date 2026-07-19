package build

import "testing"

// TestGenerics checks generics erasure against go run. A type parameter drops on
// the compiled tier and the declaration lowers once, since Python dispatches on the
// runtime value: a generic function runs whether called with inference or an
// explicit instantiation, two instantiations share one definition and one behavior,
// several type parameters erase together, a generic struct and its generic-receiver
// method lower to one class, a generic return type round trips, an interface
// constraint dispatches structurally through the value, and a generic over a slice
// appends and indexes the ordinary way.
func TestGenerics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"an inferred generic call runs",
			"package main\n\nimport \"fmt\"\n\nfunc Max[T int | float64](a, b T) T {\n\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\n\nfunc main() {\n\tfmt.Println(Max(3, 5), Max(2.5, 1.5))\n}\n",
		},
		{
			"an explicit instantiation strips its type argument",
			"package main\n\nimport \"fmt\"\n\nfunc Max[T int | float64](a, b T) T {\n\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\n\nfunc main() {\n\tfmt.Println(Max[int](3, 5), Max[float64](2.5, 1.5))\n}\n",
		},
		{
			"two instantiations share one definition",
			"package main\n\nimport \"fmt\"\n\nfunc First[T any](xs []T) T {\n\treturn xs[0]\n}\n\nfunc main() {\n\tfmt.Println(First([]int{10, 20}), First([]string{\"a\", \"b\"}))\n}\n",
		},
		{
			"several type parameters erase together",
			"package main\n\nimport \"fmt\"\n\nfunc Pair[K comparable, V any](k K, v V) {\n\tfmt.Println(k, v)\n}\n\nfunc main() {\n\tPair(1, \"x\")\n\tPair(\"k\", 2)\n}\n",
		},
		{
			"a generic struct and its method lower to one class",
			"package main\n\nimport \"fmt\"\n\ntype Box[T any] struct {\n\tV T\n}\n\nfunc (b Box[T]) Get() T {\n\treturn b.V\n}\n\nfunc main() {\n\tbi := Box[int]{V: 7}\n\tbs := Box[string]{V: \"hi\"}\n\tfmt.Println(bi.Get(), bs.Get())\n}\n",
		},
		{
			"a generic return type round trips",
			"package main\n\nimport \"fmt\"\n\ntype Box[T any] struct {\n\tV T\n}\n\nfunc Wrap[T any](v T) Box[T] {\n\treturn Box[T]{V: v}\n}\n\nfunc main() {\n\tb := Wrap(42)\n\tfmt.Println(b.V)\n}\n",
		},
		{
			"an interface constraint dispatches structurally",
			"package main\n\nimport \"fmt\"\n\ntype Stringer interface{ String() string }\n\ntype Point struct{ X, Y int }\n\nfunc (p Point) String() string {\n\treturn \"pt\"\n}\n\nfunc Show[T Stringer](x T) {\n\tfmt.Println(x.String())\n}\n\nfunc main() {\n\tShow(Point{1, 2})\n}\n",
		},
		{
			"a generic over a slice appends and indexes",
			"package main\n\nimport \"fmt\"\n\nfunc Append[T any](xs []T, v T) []T {\n\treturn append(xs, v)\n}\n\nfunc main() {\n\tfmt.Println(Append([]int{1, 2}, 3))\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestGenericsEmit pins that a generic erases in the emitted source: the function
// lowers to one def with the type parameter dropped from the signature, and an
// explicit instantiation at the call site drops its type argument, so Max[int](3,
// 5) reads as a plain call to the single erased Max.
func TestGenericsEmit(t *testing.T) {
	t.Parallel()
	src := "package main\n\nimport \"fmt\"\n\nfunc Max[T int | float64](a, b T) T {\n\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\n\nfunc main() {\n\tfmt.Println(Max[int](3, 5))\n\tfmt.Println(Max(2.5, 1.5))\n}\n"
	got := emitOf(t, src)
	wants := []string{
		"def Max(a, b):\n",
		"Max(3, 5)",
		"Max(2.5, 1.5)",
	}
	for _, w := range wants {
		if !bytesContains(got, w) {
			t.Errorf("emitted main.py = \n%s\nwant it to contain\n%s", got, w)
		}
	}
	if bytesContains(got, "Max[") {
		t.Errorf("emitted main.py still carries a type argument:\n%s", got)
	}
}
