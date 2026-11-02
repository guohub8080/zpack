package media

// GenerateTimestamps 生成截图时间点列表
// mode: "evenly"（均匀分布）| "interval"（等间隔）
func GenerateTimestamps(duration float64, count int, interval float64, mode string) []float64 {
	var timestamps []float64
	switch mode {
	case "interval":
		for t := interval; t < duration; t += interval {
			timestamps = append(timestamps, t)
		}
	case "evenly":
		if count <= 0 {
			count = 1
		}
		if count == 1 {
			return []float64{duration / 2} // 中间帧
		}
		for idx := 0; idx < count; idx++ {
			timestamps = append(timestamps, duration*float64(idx)/float64(count))
		}
	}
	return timestamps
}
