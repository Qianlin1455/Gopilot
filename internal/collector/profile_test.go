package collector

import (
	"strings"
	"testing"
)

const groupedProfileFixture = `goroutine 20 [chan receive]:
gopilot/internal/demo.(*LeakController).waitForever(0xc000100000, 0xc000200000, 0xc000300000)
	C:/repo/internal/demo/leak.go:80 +0x67
created by gopilot/internal/demo.(*LeakController).Inject in goroutine 10
	C:/repo/internal/demo/leak.go:67 +0x265

goroutine 21 [chan receive, 2 minutes]:
gopilot/internal/demo.(*LeakController).waitForever(0xc000100010, 0xc000200010, 0xc000300010)
	C:/repo/internal/demo/leak.go:80 +0x67
created by gopilot/internal/demo.(*LeakController).Inject in goroutine 10
	C:/repo/internal/demo/leak.go:67 +0x265

goroutine 22 [IO wait]:
internal/poll.runtime_pollWait(0x123, 0x72)
	C:/go/src/runtime/netpoll.go:351 +0x85
net/http.(*Server).Serve(0xc000111000, {0x1, 0xc000222000})
	C:/go/src/net/http/server.go:3360 +0x485
`

func TestParseGoroutineProfileGroupsIdenticalLeakStacks(t *testing.T) {
	parsed := ParseGoroutineProfile([]byte(groupedProfileFixture), DefaultMaxFramesPerGroup)
	if parsed.TotalGoroutines != 3 {
		t.Fatalf("total goroutines = %d, want 3", parsed.TotalGoroutines)
	}
	if len(parsed.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(parsed.Groups))
	}
	leak := parsed.Groups[0]
	if leak.Count != 2 || leak.State != "chan receive" || !leak.ContainsIntentionalLeak {
		t.Fatalf("leak group = %+v", leak)
	}
	if len(leak.Frames) < 2 || !strings.Contains(leak.Frames[0], "LeakController).waitForever") {
		t.Fatalf("leak frames = %v", leak.Frames)
	}
	if leak.Frames[1] != "created by gopilot/internal/demo.(*LeakController).Inject" {
		t.Fatalf("creator frame = %q", leak.Frames[1])
	}
}

func TestParseGoroutineProfileBoundsFrames(t *testing.T) {
	parsed := ParseGoroutineProfile([]byte(groupedProfileFixture), 1)
	for _, group := range parsed.Groups {
		if len(group.Frames) > 1 {
			t.Fatalf("group contains %d frames, want at most 1", len(group.Frames))
		}
	}
}
