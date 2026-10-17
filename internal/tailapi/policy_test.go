package tailapi

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

// What the dev cluster needs the policy to say.
var devAccess = Access{
	SSHUser:  "tailk3s",
	NodeTags: []string{"tag:dev-cp-server", "tag:dev-agent-agent"},
}

// A policy that already says everything the dev cluster needs, comments and all.
const convergedPolicy = `{
  // The nodes, and what may apply their tags.
  "tagOwners": {
    "tag:tailk3s": ["autogroup:admin"],
    "tag:dev-cp-server": ["tag:tailk3s"],
    "tag:dev-agent-agent": ["tag:tailk3s"],
  },

  "ssh": [
    { "action": "accept", "src": ["tag:tailk3s", "autogroup:admin"], "dst": ["tag:dev-cp-server", "tag:dev-agent-agent"], "users": ["tailk3s"] }
  ],

  "acls": [
    { "action": "accept", "src": ["*"], "dst": ["*:*"] }
  ]
}
`

func TestMergeLeavesAConvergedPolicyAlone(t *testing.T) {
	got, err := merge([]byte(convergedPolicy), devAccess)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != convergedPolicy {
		t.Errorf("a policy that needs nothing changed:\n%s\nwant it back unchanged:\n%s", got, convergedPolicy)
	}
}

func TestMergeAddsWhatIsMissing(t *testing.T) {
	src := `{
  "tagOwners": {
    "tag:mine": ["me@example.com"] // keep me
  },
  "acls": [
    { "action": "accept", "src": ["*"], "dst": ["*:*"] }
  ]
}
`
	got, err := merge([]byte(src), devAccess)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)

	for _, want := range []string{
		"// keep me",
		`"tag:mine": ["me@example.com"]`,
		`"tag:tailk3s": ["autogroup:admin"]`,
		`"tag:dev-cp-server": ["tag:tailk3s"]`,
		`"tag:dev-agent-agent": ["tag:tailk3s"]`,
		`"users": ["tailk3s"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %s:\n%s", want, out)
		}
	}

	// The entries the policy already had must come back before the ones we added.
	if strings.Index(out, `"tag:mine"`) > strings.Index(out, `"tag:tailk3s"`) {
		t.Errorf("an existing entry was reordered:\n%s", out)
	}

	assertStandard(t, got)
}

func TestMergeAddsDestinationsToOurOwnRule(t *testing.T) {
	// Our rule, from an earlier run with fewer nodes.
	src := `{
  "tagOwners": {
    "tag:tailk3s": ["autogroup:admin"]
  },
  "ssh": [
    { "action": "accept", "src": ["tag:tailk3s"], "dst": ["tag:old-cp-server"], "users": ["tailk3s"] }
  ]
}
`
	got, err := merge([]byte(src), devAccess)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)

	if !strings.Contains(out, `"tag:old-cp-server"`) {
		t.Errorf("the rule dropped a destination it had:\n%s", out)
	}
	for _, tag := range devAccess.NodeTags {
		if !strings.Contains(out, tag) {
			t.Errorf("the rule is missing %s:\n%s", tag, out)
		}
	}
	if n := strings.Count(out, `"users": ["tailk3s"]`); n != 1 {
		t.Errorf("added a second rule instead of extending the first: %d rules\n%s", n, out)
	}
	assertStandard(t, got)
}

func TestMergeIntoAnEmptyPolicy(t *testing.T) {
	got, err := merge([]byte(`{}`), devAccess)
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		TagOwners map[string][]string
		SSH       []struct {
			Action string
			Src    []string
			Dst    []string
			Users  []string
		}
	}
	if err := json.Unmarshal(got, &policy); err != nil {
		t.Fatalf("the result is not valid JSON: %v\n%s", err, got)
	}

	if got := policy.TagOwners[OwnerTag]; len(got) != 1 || got[0] != "autogroup:admin" {
		t.Errorf("the owner tag is %v", got)
	}
	for _, tag := range devAccess.NodeTags {
		if got := policy.TagOwners[tag]; len(got) != 1 || got[0] != OwnerTag {
			t.Errorf("%s is owned by %v, want %s", tag, got, OwnerTag)
		}
	}
	if len(policy.SSH) != 1 {
		t.Fatalf("wrote %d ssh rules, want 1", len(policy.SSH))
	}
}

func TestMergeNamesAnOutsideControlPlaneWithoutOwningIt(t *testing.T) {
	access := Access{
		SSHUser:  "tailk3s",
		NodeTags: []string{"tag:dev-agent-agent"},
		JoinTags: []string{"tag:prod-cp-server"},
	}
	got, err := merge([]byte(`{}`), access)
	if err != nil {
		t.Fatal(err)
	}

	var policy struct {
		TagOwners map[string][]string
		SSH       []struct {
			Dst []string
		}
	}
	if err := json.Unmarshal(got, &policy); err != nil {
		t.Fatalf("the result is not valid JSON: %v\n%s", err, got)
	}

	if _, ok := policy.TagOwners["tag:prod-cp-server"]; ok {
		t.Errorf("claimed ownership of a control plane tailk3s did not provision:\n%s", got)
	}
	if len(policy.SSH) != 1 {
		t.Fatalf("wrote %d ssh rules, want 1", len(policy.SSH))
	}
	if !slices.Contains(policy.SSH[0].Dst, "tag:prod-cp-server") {
		t.Errorf("the rule does not name the control plane:\n%s", got)
	}
}

func TestMergeRejectsAPolicyThatIsNotAnObject(t *testing.T) {
	if _, err := merge([]byte(`[]`), devAccess); err == nil {
		t.Error("accepted an array as a policy")
	}
}

// assertStandard checks the result is still parseable huJSON.
func assertStandard(t *testing.T, policy []byte) {
	t.Helper()
	if _, err := hujson.Parse(policy); err != nil {
		t.Errorf("the result is not valid huJSON: %v\n%s", err, policy)
	}
}
