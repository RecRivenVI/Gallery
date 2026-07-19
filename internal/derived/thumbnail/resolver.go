// Package thumbnail 提供阶段 4 唯一实现的 DerivedAsset transform：受限 JPEG 缩略图。
// 这是 internal/derivedjob.Resolver 的一条真实、可测试、无外部服务依赖的端到端派生
// 路径，用于证明阶段 3 已有的 DerivedAsset Job/缓存/lease/GC 基础具备可工作的公共契约。
// 不解析 PNG/GIF 或任何其它容器，不声称完整 ffmpeg 支持；外部工具、视频转码和波形
// 留待后续阶段。
package thumbnail

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"

	"github.com/RecRivenVI/gallery/internal/application"
	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/derived"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/media"
)

const (
	// TransformID/TransformVersion 是本 transform 唯一支持的 (id, version) 组合，
	// 是 DerivedAsset 缓存 key 的一部分——版本变化即视为不同派生结果，不复用旧缓存。
	TransformID      = "thumbnail"
	TransformVersion = "v1"

	// maxInputBytes/maxInputPixels 限制解码资源，避免用超大或畸形文件耗尽内存；
	// 均为受限解码边界，不是画质/性能调优参数，不因阶段 4 之外的需求放宽。
	maxInputBytes  = 64 << 20
	maxInputPixels = 40_000_000
	maxOutputEdge  = 512
	jpegQuality    = 82
)

// Resolver 实现 internal/derivedjob.Resolver。构造时注入的 catalog.Store 用于把
// ContentBlob 引用解析为当前 publication 内仍 present 的源文件位置，不由调用方
// 直接传入路径或 Catalog 内部 row ID。
type Resolver struct {
	catalog   *catalog.Store
	resources *application.Resources
}

func New(catalogStore *catalog.Store, resources *application.Resources) *Resolver {
	return &Resolver{catalog: catalogStore, resources: resources}
}

func (r *Resolver) Resolve(ctx context.Context, transformID, transformVersion string, blob domain.ContentBlobRef) (derived.Generator, error) {
	if transformID != TransformID || transformVersion != TransformVersion {
		return nil, fault.New(fault.CodeDerivedAssetInvalid, false, fmt.Errorf("不支持的 transform %s/%s", transformID, transformVersion))
	}
	sourceID, relativePath, size, err := r.catalog.LocateBlobFile(ctx, blob.Algorithm, blob.Digest)
	if err != nil {
		return nil, err
	}
	if size > maxInputBytes {
		return nil, fault.New(fault.CodeDerivedAssetInvalid, false, fmt.Errorf("源文件 %d 字节超出缩略图解码限额", size))
	}
	source, err := r.resources.GetSource(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, w io.Writer) (string, error) {
		snapshot, err := media.PrepareSnapshot(source.RootPath, relativePath, blob.Algorithm, blob.Digest, size, r.resources.TempRoot())
		if err != nil {
			return "", err
		}
		defer snapshot.Close()
		config, format, err := image.DecodeConfig(snapshot.File)
		if err != nil || format != "jpeg" {
			return "", fault.New(fault.CodeDerivedAssetInvalid, false, fmt.Errorf("缩略图当前只支持 JPEG 源: %w", err))
		}
		if int64(config.Width)*int64(config.Height) > maxInputPixels {
			return "", fault.New(fault.CodeDerivedAssetInvalid, false, fmt.Errorf("源图 %dx%d 像素数超出缩略图解码限额", config.Width, config.Height))
		}
		if _, err := snapshot.File.Seek(0, io.SeekStart); err != nil {
			return "", fault.New(fault.CodeInternal, true, err)
		}
		decoded, _, err := image.Decode(snapshot.File)
		if err != nil {
			return "", fault.New(fault.CodeDerivedAssetInvalid, false, err)
		}
		if err := jpeg.Encode(w, boxDownscale(decoded, maxOutputEdge), &jpeg.Options{Quality: jpegQuality}); err != nil {
			return "", fault.New(fault.CodeDerivedAssetFailed, true, err)
		}
		return "image/jpeg", nil
	}, nil
}

// boxDownscale 把 source 按最长边缩放到不超过 maxEdge，使用箱式平均（而非最近邻）
// 采样以获得可接受的缩略图画质，不依赖 stdlib 之外的图像处理库。已在边界内时原样
// 返回，不放大。
func boxDownscale(source image.Image, maxEdge int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || (width <= maxEdge && height <= maxEdge) {
		return source
	}
	scale := float64(maxEdge) / float64(width)
	if height > width {
		scale = float64(maxEdge) / float64(height)
	}
	newWidth := max(1, int(float64(width)*scale))
	newHeight := max(1, int(float64(height)*scale))
	destination := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := 0; y < newHeight; y++ {
		sourceY0 := bounds.Min.Y + y*height/newHeight
		sourceY1 := bounds.Min.Y + (y+1)*height/newHeight
		if sourceY1 <= sourceY0 {
			sourceY1 = sourceY0 + 1
		}
		for x := 0; x < newWidth; x++ {
			sourceX0 := bounds.Min.X + x*width/newWidth
			sourceX1 := bounds.Min.X + (x+1)*width/newWidth
			if sourceX1 <= sourceX0 {
				sourceX1 = sourceX0 + 1
			}
			var rSum, gSum, bSum, aSum, count uint64
			for sy := sourceY0; sy < sourceY1 && sy < bounds.Max.Y; sy++ {
				for sx := sourceX0; sx < sourceX1 && sx < bounds.Max.X; sx++ {
					r, g, b, a := source.At(sx, sy).RGBA()
					rSum += uint64(r)
					gSum += uint64(g)
					bSum += uint64(b)
					aSum += uint64(a)
					count++
				}
			}
			if count == 0 {
				count = 1
			}
			destination.SetRGBA64(x, y, color.RGBA64{
				R: uint16(rSum / count), G: uint16(gSum / count), B: uint16(bSum / count), A: uint16(aSum / count),
			})
		}
	}
	return destination
}
