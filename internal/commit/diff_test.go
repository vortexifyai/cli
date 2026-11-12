package commit

import (
	"testing"

	"vortexify-cli/internal/api"
	"vortexify-cli/internal/resources"
)

func TestFindChanges(t *testing.T) {
	local := []resources.Resource{{Type: "api", Name: "same", Hash: "a"}, {Type: "api", Name: "changed", Hash: "b"}, {Type: "sync", Name: "new", Hash: "c"}}
	remote := []api.Resource{{ID: "1", Type: "api", Name: "same", CodeHash: "a", Version: 1}, {ID: "2", Type: "api", Name: "changed", CodeHash: "a", Version: 1}, {ID: "3", Type: "api", Name: "absent", CodeHash: "a", Version: 1}}
	changes := FindChanges(local, remote)
	if len(changes) != 2 || changes[0].Remote.ID != "2" || changes[1].Remote != nil {
		t.Fatalf("changes %#v", changes)
	}
}

func TestFindAttachmentOnlyChanges(t *testing.T) {
	local := []resources.Resource{{Type: "agent", Name: "support", Hash: "same"}}
	remote := []api.Resource{{ID: "agent", Type: "agent", Name: "support", CodeHash: "same", Version: 1, Files: &resources.FileStatus{Changed: true}}}
	if len(FindChanges(local, remote)) != 1 {
		t.Fatal("skipped attachment-only changes")
	}
	remote[0].Files.Changed = false
	if len(FindChanges(local, remote)) != 0 {
		t.Fatal("selected committed attachments")
	}
}

func TestUnchangedStarterStillNeedsFirstCommit(t *testing.T) {
	local := []resources.Resource{{Type: "api", Name: "starter", Hash: "same"}}
	remote := []api.Resource{{ID: "api", Type: "api", Name: "starter", CodeHash: "same", Version: 0}}
	if len(FindChanges(local, remote)) != 1 {
		t.Fatal("uncommitted starter was skipped")
	}
}
