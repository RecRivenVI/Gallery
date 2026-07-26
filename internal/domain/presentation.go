package domain

// Badge 是规则派生的作品角标，跨规则、扫描、Catalog、查询与传输四层流转，因此定义在
// 领域层而不是任何单一层。
//
// 它是 **Source-derived 展示事实**：完全由规则与该作品的 metadata/媒体构成决定，随重扫
// 重新计算，不进 control.db，也不与用户 Overlay 合并。出现条件与配色都在规则中裁决完毕，
// 客户端只按 Position 渲染，不得再自行推导条件或颜色——这正是「规则是 Source 差异的唯一
// 解释入口」在展示层的体现。
//
// Color/Background/Border 是深色主题取值，*Light 是浅色主题取值；某一侧缺省时由客户端
// 沿用其设计系统的默认值，服务端不代为填充，避免把主题决策固化进快照。
type Badge struct {
	ID              string `json:"id"`
	Order           int    `json:"order"`
	Position        string `json:"position"`
	Label           string `json:"label"`
	Color           string `json:"color,omitempty"`
	Background      string `json:"background,omitempty"`
	Border          string `json:"border,omitempty"`
	ColorLight      string `json:"colorLight,omitempty"`
	BackgroundLight string `json:"backgroundLight,omitempty"`
	BorderLight     string `json:"borderLight,omitempty"`
}
