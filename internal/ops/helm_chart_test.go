package ops

import "testing"

func TestRenderHelmChartManifest(t *testing.T) {
	manifest := renderHelmChartManifest(InstallHelmChartArgs{
		ReleaseName: "cloudnativepg",
		ChartSource: "cloudnative-pg",
		Namespace:   "cnpg-system",
		Repo:        "https://cloudnative-pg.github.io/charts",
		Values:      "monitoring:\n  enabled: false",
	})
	for _, want := range []string{
		"kind: HelmChart",
		"name: cloudnativepg",
		"chart: cloudnative-pg",
		"repo: https://cloudnative-pg.github.io/charts",
		"targetNamespace: cnpg-system",
		"valuesContent: |",
		"enabled: false",
	} {
		if !contains(manifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, manifest)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
