package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const BlobAlgorithmSHA256V1 = "sha256-v1"

// ContentBlobRef 是跨 Catalog revision 稳定的内容引用，不是数据库行号。
type ContentBlobRef struct {
	Algorithm string `json:"algorithm"`
	Digest    string `json:"digest"`
}

func NewSHA256BlobRef(sum [sha256.Size]byte) ContentBlobRef {
	return ContentBlobRef{Algorithm: BlobAlgorithmSHA256V1, Digest: hex.EncodeToString(sum[:])}
}

func ParseContentBlobRef(algorithm, digest string) (ContentBlobRef, error) {
	if algorithm != BlobAlgorithmSHA256V1 {
		return ContentBlobRef{}, fmt.Errorf("不支持的 Blob hash algorithm")
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return ContentBlobRef{}, fmt.Errorf("Blob digest 必须是完整 SHA-256")
	}
	return ContentBlobRef{Algorithm: algorithm, Digest: digest}, nil
}

// FileLocationRef 只描述 Source 内位置身份；路径和内部 Catalog row ID 都不是永久引用。
type FileLocationRef struct {
	SourceID        ID     `json:"sourceId"`
	IdentityVersion uint16 `json:"identityVersion"`
	LocationKey     string `json:"locationKey"`
}

func NewFileLocationRef(sourceID ID, identityVersion uint16, locationKey string) (FileLocationRef, error) {
	if sourceID.Kind() != IDSource {
		return FileLocationRef{}, fmt.Errorf("FileLocation 必须引用 Source ID")
	}
	if identityVersion == 0 || locationKey == "" {
		return FileLocationRef{}, fmt.Errorf("FileLocation identity version 和 key 不能为空")
	}
	return FileLocationRef{SourceID: sourceID, IdentityVersion: identityVersion, LocationKey: locationKey}, nil
}
