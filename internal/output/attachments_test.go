package output

import (
	"bytes"
	"strings"
	"testing"
	"vortexify-cli/internal/resources"
)

func TestAttachmentStatus(t *testing.T) {
	var out bytes.Buffer
	files := &resources.FileStatus{Changed: true, Total: 1, Added: []resources.AttachedFile{{ID: "new", Name: "shipping-policy.md"}}, Removed: []resources.AttachedFile{{ID: "old", Name: "old.md"}}}
	if err := PrintStatus(&out, []ResourceStatus{{Name: "agent:support", Registration: "registered", Change: "clean", Files: files}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Code: clean", "Attachments: 1 added, 1 removed — uncommitted", "    + shipping-policy.md", "    - old.md"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
}

func TestNewResourceStatusOffersRegistrationOrCommit(t *testing.T) {
	var out bytes.Buffer
	if err := PrintStatus(&out, []ResourceStatus{{Name: "sync:postgres/items", Registration: "not registered", Change: "new"}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vortexify create sync postgres/items (register)", "vortexify commit (register and upload)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Error:") {
		t.Fatal("unregistered resource shown as an error", out.String())
	}
}
