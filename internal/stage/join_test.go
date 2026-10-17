package stage

import "testing"

func TestJoinSettings(t *testing.T) {
	one := joinSettings(Join{Servers: []string{"100.64.0.1"}, Token: "K10abc"})
	want := "server: \"https://100.64.0.1:6443\"\ntoken: \"K10abc\"\n"
	if one != want {
		t.Errorf("one control plane =\n%s\nwant\n%s", one, want)
	}

	// Every control plane goes in, so a node is not tied to one being up.
	three := joinSettings(Join{Servers: []string{"100.64.0.1", "100.64.0.2", "100.64.0.3"}, Token: "K10abc"})
	want = "server: \"https://100.64.0.1:6443,https://100.64.0.2:6443,https://100.64.0.3:6443\"\ntoken: \"K10abc\"\n"
	if three != want {
		t.Errorf("three control planes =\n%s\nwant\n%s", three, want)
	}
}
