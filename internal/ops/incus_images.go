package ops

import "strings"

// normalizeIncusLaunchImage maps product image refs (ubuntu:22.04) to Incus CLI launch syntax.
// Incus has no "ubuntu" remote — images come from the "images" simplestreams remote.
func normalizeIncusLaunchImage(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return "images:ubuntu/22.04"
	}
	if strings.HasPrefix(image, "images:") || strings.HasPrefix(image, "local:") {
		return image
	}
	colon := strings.Index(image, ":")
	if colon <= 0 {
		return image
	}
	remote := image[:colon]
	alias := image[colon+1:]
	if remote == "ubuntu" {
		if strings.Contains(alias, "/") {
			return "images:" + alias
		}
		return "images:ubuntu/" + alias
	}
	return image
}
