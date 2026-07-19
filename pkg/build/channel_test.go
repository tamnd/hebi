package build

import (
	"path/filepath"
	"testing"
)

// TestChannel checks the unbuffered channel surface against go run. An unbuffered
// channel is a rendezvous: a send blocks until a receiver takes the value in the
// same instant, so the handoff and the happens-before edge it establishes are
// what make these programs deterministic without a sleep. A channel used as a
// done signal orders a goroutine's work before the line that waits on it, a value
// handed across arrives intact, and several sends over one channel arrive in
// order because each send waits for its receive.
func TestChannel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"an unbuffered channel hands a value across",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int)\n\tgo func() {\n\t\tch <- 42\n\t}()\n\tv := <-ch\n\tfmt.Println(v)\n}\n",
		},
		{
			"a done channel orders the goroutine's work before the wait",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tdone := make(chan bool)\n\tgo func() {\n\t\tfmt.Println(\"work\")\n\t\tdone <- true\n\t}()\n\t<-done\n\tfmt.Println(\"finished\")\n}\n",
		},
		{
			"several sends over one channel arrive in order",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int)\n\tgo func() {\n\t\tfor i := 0; i < 3; i++ {\n\t\t\tch <- i\n\t\t}\n\t}()\n\tfmt.Println(<-ch, <-ch, <-ch)\n}\n",
		},
		{
			"a struct value handed across arrives with its fields",
			"package main\n\nimport \"fmt\"\n\ntype Point struct {\n\tX int\n\tY int\n}\n\nfunc main() {\n\tch := make(chan Point)\n\tgo func() {\n\t\tch <- Point{X: 3, Y: 4}\n\t}()\n\tp := <-ch\n\tfmt.Println(p.X, p.Y)\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestChannelEmit pins the emitted forms. make of an unbuffered channel builds a
// Chan with capacity zero and a zero factory for the element, a send routes
// through the send helper, and a one-value receive routes through the receive
// helper that drops the ok flag.
func TestChannelEmit(t *testing.T) {
	t.Parallel()
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int)\n\tch <- 1\n\tfmt.Println(<-ch)\n}\n"
	got := emitOf(t, src)
	for _, want := range []string{
		"_hebirt.Chan(0, lambda: 0)",
		"_hebirt.chan_send(ch, 1)",
		"_hebirt.chan_recv(ch)",
	} {
		if !bytesContains(got, want) {
			t.Errorf("channel emit missing %q:\n%s", want, got)
		}
	}
}

// TestBufferedChannelDiagnosed pins that a buffered channel is diagnosed rather
// than silently treated as unbuffered, since its capacity changes the blocking
// behavior and it lands on its own slice.
func TestBufferedChannelDiagnosed(t *testing.T) {
	t.Parallel()
	src := writeModule(t, "package main\n\nfunc main() {\n\tch := make(chan int, 4)\n\t_ = ch\n}\n")
	out := filepath.Join(t.TempDir(), "out")
	if _, err := Build(src, out); err == nil {
		t.Fatal("expected a diagnostic for a buffered channel, got none")
	}
}
