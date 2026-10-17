package stage

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// What k3s writes to /etc/rancher/k3s/k3s.yaml, trimmed to the fields that matter.
const k3sKubeconfig = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLUNBLQ==
    server: https://127.0.0.1:6443
  name: default
contexts:
- context:
    cluster: default
    user: default
  name: default
current-context: default
kind: Config
preferences: {}
users:
- name: default
  user:
    client-certificate-data: LS0tQ0VSVA==
    client-key-data: LS0tS0VZ
`

type kubeconfigDoc struct {
	Clusters []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server string `yaml:"server"`
			CAData string `yaml:"certificate-authority-data"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster string `yaml:"cluster"`
			User    string `yaml:"user"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			CertData string `yaml:"client-certificate-data"`
			KeyData  string `yaml:"client-key-data"`
		} `yaml:"user"`
	} `yaml:"users"`
	CurrentContext string `yaml:"current-context"`
}

func TestRewrite(t *testing.T) {
	out, err := rewrite(k3sKubeconfig, "https://100.64.0.1:6443", "cp1")
	if err != nil {
		t.Fatal(err)
	}

	var doc kubeconfigDoc
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid yaml: %v\n%s", err, out)
	}

	cluster := doc.Clusters[0]
	checks := []struct {
		what string
		got  string
		want string
	}{
		{"server", cluster.Cluster.Server, "https://100.64.0.1:6443"},
		{"cluster name", cluster.Name, "cp1"},
		{"context name", doc.Contexts[0].Name, "cp1"},
		{"context cluster", doc.Contexts[0].Context.Cluster, "cp1"},
		{"context user", doc.Contexts[0].Context.User, "cp1"},
		{"user name", doc.Users[0].Name, "cp1"},
		{"current context", doc.CurrentContext, "cp1"},
		{"certificate authority kept", cluster.Cluster.CAData, "LS0tLUNBLQ=="},
		{"client certificate kept", doc.Users[0].User.CertData, "LS0tQ0VSVA=="},
		{"client key kept", doc.Users[0].User.KeyData, "LS0tS0VZ"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.what, c.got, c.want)
		}
	}
}
