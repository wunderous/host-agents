package kubernetes

import "testing"

func TestRenderHelmTemplateRequiresArgs(t *testing.T) {
	_, err := New(nil, Deps{}).RenderHelmTemplate(RenderHelmTemplateArgs{}, nil)
	if err == nil {
		t.Fatal("expected error for empty args")
	}
}
