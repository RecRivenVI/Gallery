// synth：全新合成测试素材生成器。不读取任何旧数据、不模仿旧 catalog 的表结构,
// 只从"产品概念"出发造一批可控的作品/媒体/创作者记录,供各决策原型共用。
package synth

import (
	"fmt"
	"hash/fnv"
	"math/rand"
)

// Work 是从产品概念出发的最小作品视图(不是任何旧表的映射):
// 一个作品有稳定身份 ID、归属创作者、多语言标题、标签、发布时间与若干媒体。
type Work struct {
	ID          string
	CreatorID   string
	CreatorName string
	Title       string
	Tags        []string
	PublishedAt string // RFC3339
	Media       []Media
}

type Media struct {
	// 合成媒体身份：使用与物理路径无关的确定性内容种子。
	// 这只为原型提供稳定 ID，不声称等价于真实文件的内容哈希；生产身份策略由 P15 验证。
	ID       string
	RelPath  string
	Size     int64
	Kind     string // image|video
	Language string // 供搜索多语言测试
}

var (
	titlesZh = []string{"星空下的约定", "夏日回忆", "机械少女", "樱花飘落时", "深海之城", "龙与勇者", "雨后彩虹", "月光协奏曲"}
	titlesJa = []string{"星空の約束", "夏の思い出", "機械少女", "桜散る頃", "深海都市", "竜と勇者", "雨上がりの虹", "月光ソナタ"}
	titlesEn = []string{"Starlit Promise", "Summer Memories", "Mechanical Girl", "When Cherry Falls", "Abyssal City", "Dragon and Hero", "Rainbow After Rain", "Moonlight Sonata"}
	tagPool  = []string{"原创", "オリジナル", "original", "风景", "风景", "sci-fi", "fantasy", "R-18", "4K", "线稿", "彩色", "插画"}
)

func h(s string) string {
	f := fnv.New64a()
	_, _ = f.Write([]byte(s))
	return fmt.Sprintf("%016x", f.Sum64())
}

// Generate 造 n 个作品,creators 个创作者;seed 固定 → 可复现。
func Generate(n, creators int, seed int64) []Work {
	rng := rand.New(rand.NewSource(seed))
	works := make([]Work, n)
	for i := 0; i < n; i++ {
		cid := fmt.Sprintf("creator-%04d", rng.Intn(creators))
		lang := []string{"zh", "ja", "en"}[i%3]
		var title string
		switch lang {
		case "zh":
			title = fmt.Sprintf("%s %d", titlesZh[i%len(titlesZh)], i)
		case "ja":
			title = fmt.Sprintf("%s %d", titlesJa[i%len(titlesJa)], i)
		default:
			title = fmt.Sprintf("%s %d", titlesEn[i%len(titlesEn)], i)
		}
		wid := h(fmt.Sprintf("%s/%d", cid, i))
		nTags := 2 + rng.Intn(3)
		tags := make([]string, nTags)
		for t := range tags {
			tags[t] = tagPool[rng.Intn(len(tagPool))]
		}
		nMedia := 1 + rng.Intn(8)
		media := make([]Media, nMedia)
		for m := range media {
			kind := "image"
			if rng.Intn(10) == 0 {
				kind = "video"
			}
			size := int64(100_000 + rng.Intn(5_000_000))
			rel := fmt.Sprintf("%s/%s/%d.%s", cid, wid[:8], m+1, ext(kind))
			media[m] = Media{
				ID:       h(fmt.Sprintf("content|%s|%d|%d", wid, size, m)),
				RelPath:  rel,
				Size:     size,
				Kind:     kind,
				Language: lang,
			}
		}
		works[i] = Work{
			ID: wid, CreatorID: cid, CreatorName: "作者" + cid[8:],
			Title: title, Tags: tags,
			PublishedAt: fmt.Sprintf("2026-%02d-%02dT%02d:00:00Z", 1+i%12, 1+i%28, i%24),
			Media:       media,
		}
	}
	return works
}

func ext(kind string) string {
	if kind == "video" {
		return "mp4"
	}
	return "jpg"
}

// Mutate 模拟一次增量:改动 changed 比例的作品标题、新增 added 个、删除 removed 个。
// 返回新的作品集(用于喂给增量扫描)。
func Mutate(base []Work, changedFrac float64, added, removed int, seed int64) []Work {
	rng := rand.New(rand.NewSource(seed))
	out := make([]Work, 0, len(base)+added)
	// 删除末尾 removed 个
	keep := base
	if removed > 0 && removed < len(base) {
		keep = base[:len(base)-removed]
	}
	for _, w := range keep {
		if rng.Float64() < changedFrac {
			w.Title = w.Title + "(改)"
			if len(w.Media) > 0 {
				w.Media[0].Size += 1 // 内容变 → 媒体身份变
				w.Media[0].ID = h(fmt.Sprintf("%s|%d|mut", w.Media[0].RelPath, w.Media[0].Size))
			}
		}
		out = append(out, w)
	}
	for a := 0; a < added; a++ {
		cid := fmt.Sprintf("creator-%04d", rng.Intn(50))
		wid := h(fmt.Sprintf("added/%d/%d", seed, a))
		out = append(out, Work{
			ID: wid, CreatorID: cid, CreatorName: "作者" + cid[8:],
			Title: fmt.Sprintf("新增作品 %d", a), Tags: []string{"new"},
			PublishedAt: "2026-12-31T00:00:00Z",
			Media:       []Media{{ID: h(wid), RelPath: cid + "/" + wid[:8] + "/1.jpg", Size: 123456, Kind: "image", Language: "zh"}},
		})
	}
	return out
}
