package stage

import "testing"

const wantServerTailnet = `node-name: "cp1-6694"
node-ip: "100.64.0.1"
node-external-ip: "1.2.3.4"
flannel-iface: tailscale0
write-kubeconfig-mode: "0600"
cluster-init: true
advertise-address: "100.64.0.1"
tls-san:
  - 100.64.0.1
  - cp1
`

const wantServerJoining = `node-name: "cp2-6694"
node-ip: "100.64.0.1"
node-external-ip: "1.2.3.4"
flannel-iface: tailscale0
write-kubeconfig-mode: "0600"
advertise-address: "100.64.0.1"
tls-san:
  - 100.64.0.1
  - cp2
server: "https://100.64.0.1:6443"
token: "K10abc"
`

const wantAgentTailnet = `node-name: "w1-0a29"
node-ip: "100.64.0.2"
node-external-ip: "5.6.7.8"
flannel-iface: tailscale0
server: "https://100.64.0.1:6443"
token: "K10abc"
`

type configCase struct {
	name      string
	k3s       K3s
	tailnetIP string
	join      string
	want      string
}

const wantJoin = "server: \"https://100.64.0.1:6443\"\ntoken: \"K10abc\"\n"

// What the orchestrator hands a joining node.
var testJoin = Join{Servers: []string{"100.64.0.1"}, Token: "K10abc"}

func TestK3sConfig(t *testing.T) {
	server := configCase{
		name:      "server on the tailnet",
		k3s:       K3s{Role: "server", Hostname: "cp1", NodeName: "cp1-6694", Host: "1.2.3.4"},
		tailnetIP: "100.64.0.1",
		want:      wantServerTailnet,
	}

	serverJoining := configCase{
		name:      "control plane joining a cluster",
		k3s:       K3s{Role: "server", Hostname: "cp2", NodeName: "cp2-6694", Host: "1.2.3.4", Join: testJoin},
		tailnetIP: "100.64.0.1",
		join:      wantJoin,
		want:      wantServerJoining,
	}

	agent := configCase{
		name:      "agent on the tailnet",
		k3s:       K3s{Role: "agent", Hostname: "w1", NodeName: "w1-0a29", Host: "5.6.7.8", Join: testJoin},
		tailnetIP: "100.64.0.2",
		join:      wantJoin,
		want:      wantAgentTailnet,
	}

	for _, tc := range []configCase{server, serverJoining, agent} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.k3s.config(tc.tailnetIP, tc.join); got != tc.want {
				t.Errorf("config() =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
