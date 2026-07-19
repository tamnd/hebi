package build

import (
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

// TestBufferedChannel checks the buffered surface against go run. A buffered
// channel lets a sender deposit up to the capacity without a waiting receiver, so
// a producer can run ahead, and a full channel blocks the next send until a
// receive frees a slot. close then lets a range drain what is buffered and stop.
func TestBufferedChannel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a buffered channel takes several sends before any receive",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int, 3)\n\tch <- 1\n\tch <- 2\n\tch <- 3\n\tfmt.Println(<-ch, <-ch, <-ch)\n}\n",
		},
		{
			"a range over a closed buffered channel drains it and stops",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int, 3)\n\tch <- 1\n\tch <- 2\n\tch <- 3\n\tclose(ch)\n\ttotal := 0\n\tfor v := range ch {\n\t\ttotal += v\n\t}\n\tfmt.Println(total)\n}\n",
		},
		{
			"a full buffered channel blocks the sender until a receiver frees a slot",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int, 1)\n\tdone := make(chan bool)\n\tgo func() {\n\t\tch <- 1\n\t\tch <- 2\n\t\tclose(ch)\n\t\tdone <- true\n\t}()\n\tfmt.Println(<-ch)\n\tfmt.Println(<-ch)\n\t<-done\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestChannelCloseAndCommaOk checks close and the two-value receive against go
// run. A comma-ok receive reports whether the channel was still open, so a reader
// can tell a real zero from a closed channel, and a range over a closed channel
// stops once it has drained the buffer.
func TestChannelCloseAndCommaOk(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a comma-ok receive reports open then closed",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int, 1)\n\tch <- 7\n\tclose(ch)\n\tv, ok := <-ch\n\tfmt.Println(v, ok)\n\tv, ok = <-ch\n\tfmt.Println(v, ok)\n}\n",
		},
		{
			"a range over a closed unbuffered channel sees every sent value",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int)\n\tgo func() {\n\t\tfor i := 0; i < 4; i++ {\n\t\t\tch <- i\n\t\t}\n\t\tclose(ch)\n\t}()\n\tsum := 0\n\tfor v := range ch {\n\t\tsum += v\n\t}\n\tfmt.Println(sum)\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestBufferedChannelEmit pins the buffered forms: make of a buffered channel
// carries its capacity, close routes through the close helper, a comma-ok receive
// keeps the ok flag through the two-value helper, and a range over a channel
// becomes a while loop that receives and breaks on the ok flag.
func TestBufferedChannelEmit(t *testing.T) {
	t.Parallel()
	src := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tch := make(chan int, 2)\n\tch <- 1\n\tclose(ch)\n\tv, ok := <-ch\n\tfmt.Println(v, ok)\n\tfor x := range ch {\n\t\tfmt.Println(x)\n\t}\n}\n"
	got := emitOf(t, src)
	for _, want := range []string{
		"_hebirt.Chan(2, lambda: 0)",
		"_hebirt.chan_close(ch)",
		"_hebirt.chan_recv_ok(ch)",
	} {
		if !bytesContains(got, want) {
			t.Errorf("buffered channel emit missing %q:\n%s", want, got)
		}
	}
}

// TestCloseClosedChannelCrashes pins that closing an already closed channel
// crashes the Go way, a GoPanic that escapes main and exits two, rather than
// passing silently.
func TestCloseClosedChannelCrashes(t *testing.T) {
	t.Parallel()
	assertProgramCrashesLikeGo(t, "package main\n\nfunc main() {\n\tch := make(chan int)\n\tclose(ch)\n\tclose(ch)\n}\n")
}

// TestSendOnClosedChannelCrashes pins that a send on a closed channel crashes the
// Go way rather than depositing into a closed buffer.
func TestSendOnClosedChannelCrashes(t *testing.T) {
	t.Parallel()
	assertProgramCrashesLikeGo(t, "package main\n\nfunc main() {\n\tch := make(chan int, 1)\n\tclose(ch)\n\tch <- 1\n}\n")
}
