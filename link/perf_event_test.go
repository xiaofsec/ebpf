package link

import (
	"testing"

	"github.com/xiaofsec/ebpf/internal/testutils"
)

func TestHaveBPFLinkPerfEvent(t *testing.T) {
	testutils.CheckFeatureTest(t, haveBPFLinkPerfEvent)
}
