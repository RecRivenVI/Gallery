package identity

import (
	"crypto/rand"
	"fmt"
	"io"

	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
)

type Generator struct {
	Clock  ports.Clock
	Random io.Reader
}

func NewGenerator(clock ports.Clock) Generator {
	return Generator{Clock: clock, Random: rand.Reader}
}

func (g Generator) New(kind domain.IDKind) (domain.ID, error) {
	if g.Clock == nil {
		return domain.ID{}, fmt.Errorf("ID generator 缺少 Clock")
	}
	random := g.Random
	if random == nil {
		random = rand.Reader
	}

	var raw [16]byte
	ms := uint64(g.Clock.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		raw[i] = byte(ms)
		ms >>= 8
	}
	if _, err := io.ReadFull(random, raw[6:]); err != nil {
		return domain.ID{}, fmt.Errorf("生成 UUIDv7 随机部分: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	return domain.IDFromUUIDv7(kind, raw)
}
