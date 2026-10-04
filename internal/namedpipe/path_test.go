package namedpipe

import "testing"

func TestNamedPipePathsRejectTraversalAndAmbiguousHosts(t *testing.T) {
	for _, path := range []string{`\\.\pipe\branch_one`, `\\.\pipe\ops-2`} {
		if err := ValidateLocal(path); err != nil {
			t.Errorf("local %q: %v", path, err)
		}
	}
	for _, path := range []string{`\\PARENT01\pipe\branch_one`, `\\192.168.1.5\pipe\ops-2`, `\\.\pipe\same_host`} {
		if err := ValidateRemote(path); err != nil {
			t.Errorf("remote %q: %v", path, err)
		}
	}
	for _, path := range []string{`\\.\pipe\..\evil`, `\\.\pipe\bad/name`, `\\.\pipe\`} {
		if ValidateLocal(path) == nil {
			t.Errorf("accepted invalid local path %q", path)
		}
	}
	for _, path := range []string{`\\HOST\share\name`, `\\HOST\pipe\..\evil`, `\\HOST\pipe\bad/name`, `\\HOST NAME\pipe\name`} {
		if ValidateRemote(path) == nil {
			t.Errorf("accepted invalid remote path %q", path)
		}
	}
}
