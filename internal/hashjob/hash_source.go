//go:build !gallery_e2e_testhooks

package hashjob

import "github.com/RecRivenVI/gallery/internal/media"

func hashSourceFileWithOptions(root, relative string, options media.HashOptions) (media.HashResult, error) {
	return media.HashSourceFileWithOptions(root, relative, options)
}
