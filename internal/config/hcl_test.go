package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.hcl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFromFile(t *testing.T) {
	t.Setenv("TEST_INIT_PASSWORD", "hunter2")

	nodes, err := FromFile(write(t, `
node "cp1" {
  host          = "1.2.3.4"
  role          = "server"
  init_password = env("TEST_INIT_PASSWORD")
}

node "w1" {
  host             = "5.6.7.8"
  role             = "agent"
  join             = "cp1"
  disable_services = ["multipathd"]
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}

	cp1, w1 := nodes[0], nodes[1]
	checks := []struct {
		what string
		got  string
		want string
	}{
		{"cp1 name", cp1.Name, "cp1"},
		{"cp1 host", cp1.Host, "1.2.3.4"},
		{"cp1 role", cp1.Role, "server"},
		{"cp1 init password from env", cp1.InitPassword, "hunter2"},
		{"w1 name", w1.Name, "w1"},
		{"w1 join", w1.Join, "cp1"},
		{"w1 sets no init user", w1.InitUser, ""},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.what, c.got, c.want)
		}
	}

	if len(w1.DisableServices) != 1 || w1.DisableServices[0] != "multipathd" {
		t.Errorf("w1 disable_services = %v, want [multipathd]", w1.DisableServices)
	}
}

// Node blocks are self-contained, so settings outside them have nowhere to go.
func TestFromFileRejectsGlobalSettings(t *testing.T) {
	_, err := FromFile(write(t, `
admin_user = "ops"

node "cp1" {
  host = "1.2.3.4"
  role = "server"
}
`))
	if err == nil {
		t.Error("want an error for a setting outside a node block")
	}
}
