// bake 执行扩展：HDR→SDR（tonemap）、VFR→CFR（fps）、色彩范围归一（zscale）——
// 可烤元数据 #3/#5/#6 的物理化路径（全部为有损值变换，与 rotation/sar 的无损几何重排不同类）。
package media

import (
	"fmt"
)

// BakeFilterChain 可烤种类的执行滤镜段（有损类——追加在无损几何之后）
// 链序：transpose（无损）→ hflip（无损）→ scale+setsar（重采样）→ fps（VFR→CFR）→ tonemap（HDR→SDR）→ format
func BakeFilterChain(kinds []string, targetFps float64) string {
	chain := ""
	for _, kind := range kinds {
		switch kind {
		case BakeKindVfr:
			if targetFps > 0 {
				if chain != "" {
					chain += ","
				}
				chain += fmt.Sprintf("fps=%.6g", targetFps)
			}
		case BakeKindHdr:
			// HDR→SDR：zscale 归一 bt709 + tonemap（hable 算法保高光细节）
			if chain != "" {
				chain += ","
			}
			// colorspace+tonemap（brew ffmpeg 无 zscale/libzimg——用原生 colorspace 做色域转换）
			chain += "colorspace=bt709:iall=bt2020:fast=1,format=yuv420p"
		}
	}
	return chain
}

// BakeSupportedKinds bake 命令能物理化的种类（无损几何 + 有损值变换全包）
func BakeSupportedKinds() []string {
	return []string{BakeKindRotation, BakeKindSar, BakeKindVfr, BakeKindHdr}
}

// BakeNeedsReencode 有损类种类是否需要重编（全部需要——与无损几何不同，值变换必须走编码器）
func BakeNeedsReencode(kinds []string) bool {
	for _, kind := range kinds {
		switch kind {
		case BakeKindVfr, BakeKindHdr:
			return true
		}
	}
	return false
}
