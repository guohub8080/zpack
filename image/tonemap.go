// HDR tone mapping——纯函数层（cgo/纯构建共用，表驱动单测覆盖数学）。
package image

// HDR_TONEMAP_THRESHOLD 场景最大值 ≤ 此值视为 SDR 范围直通（不压曲线——
// 保护常规图 roundtrip 的逐字节一致性；真 HDR 高亮才滚降）
const HDR_TONEMAP_THRESHOLD = 1.05

// TonemapReinhard 扩展 Reinhard 曲线：白点 W 映射到 1.0，高亮平滑滚降不硬裁。
// 逐通道作用（v1——亮度域作用需色彩空间换算，缓期）；alpha 通道不参与。
func TonemapReinhard(value, whitePoint float32) float32 {
	if whitePoint <= HDR_TONEMAP_THRESHOLD {
		return value
	}
	return value * (1 + value/(whitePoint*whitePoint)) / (1 + value)
}

// NeedsTonemap 场景是否超出 SDR 范围（决定走直通还是压曲线）
func NeedsTonemap(sceneMax float32) bool {
	return sceneMax > HDR_TONEMAP_THRESHOLD
}

// FloatToByte 浮点亮度 → 8bit（clamp + 半值进位；确定性舍入便于交叉验证）
func FloatToByte(value float32) uint8 {
	scaled := value*255 + 0.5
	if scaled <= 0 {
		return 0
	}
	if scaled >= 255 {
		return 255
	}
	return uint8(scaled)
}
