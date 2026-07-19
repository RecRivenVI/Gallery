package thumbnail

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

func TestBoxDownscaleLimitsLongestEdgeAndPreservesAspect(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 1024, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 1024; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0, A: 255})
		}
	}
	scaled := boxDownscale(source, 256)
	bounds := scaled.Bounds()
	if bounds.Dx() != 256 || bounds.Dy() != 128 {
		t.Fatalf("缩放尺寸 = %dx%d，want 256x128（保持 2:1 纵横比）", bounds.Dx(), bounds.Dy())
	}
}

func TestBoxDownscalePassesThroughWhenAlreadyWithinLimit(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 100, 50))
	scaled := boxDownscale(source, 512)
	if scaled != image.Image(source) {
		t.Fatal("已在限额内的图像不应被重新采样")
	}
}

func TestResolveRejectsUnknownTransform(t *testing.T) {
	resolver := New(nil, nil)
	if _, err := resolver.Resolve(context.Background(), "other", "v1", domain.ContentBlobRef{Algorithm: "sha256-v1", Digest: mustDigest()}); !isCode(err, fault.CodeDerivedAssetInvalid) {
		t.Fatalf("未知 transform 应返回 CodeDerivedAssetInvalid, got %v", err)
	}
}

func mustDigest() string {
	return "0000000000000000000000000000000000000000000000000000000000000000000000000"[:64]
}

func isCode(err error, code fault.Code) bool {
	structured, ok := err.(*fault.Error)
	return ok && structured.Code == code
}
