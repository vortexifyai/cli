package projectfiles

import (
	"bytes"
	"strings"
	"testing"
)

func TestSourceLinkWarningsPreservePortableLinks(t *testing.T) {
	var out bytes.Buffer
	text := "[Order](../../knowledge-base/entities/order.md)\n[Transform](vortexify://transform/forecast)\n[web](https://example.com)\n[Source](../../../custom/transforms/forecast/main.py)\n[source-ref]: ../../../apis/orders/main.py\n```md\n[Example](../../../apps/demo/main.py)\n```\n`[inline](../../../apps/demo/main.py)`"
	warnSourceLinks(&out, "context", "architecture/pipeline.md", text)
	if strings.Count(out.String(), "Warning:") != 2 || !strings.Contains(out.String(), "custom/transforms") || !strings.Contains(out.String(), "apis/orders") {
		t.Fatal(out.String())
	}
}
