package hatch

import (
	"os"
	"testing"
)

func TestProbeGeneratedMP4(t *testing.T) {
	path := "../../../../data/media/80c5aa15f2119927defac06650507efe.mp4"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	durationMS, width, height, ok := probeMP4Metadata(data)
	if !ok {
		t.Fatalf("probe failed on a generated MP4")
	}
	if durationMS <= 0 || width <= 0 || height <= 0 {
		t.Fatalf("invalid metadata: %dms %dx%d", durationMS, width, height)
	}
	t.Logf("duration=%dms size=%dx%d", durationMS, width, height)
}
