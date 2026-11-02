package image

import "testing"

func TestTonemapReinhard(t *testing.T) {
	tests := []struct {
		name       string
		value      float32
		whitePoint float32
		want       float32
	}{
		{"白点映射到 1.0", 4.0, 4.0, 1.0},
		{"中值 2→0.75", 2.0, 4.0, 0.75},
		{"1→0.53125", 1.0, 4.0, 0.53125},
		{"SDR 范围直通", 0.5, 1.0, 0.5},
		{"零值稳定", 0.0, 8.0, 0.0},
		{"白点恰好阈值直通", 1.0, 1.05, 1.0},
	}
	for _, tt := range tests {
		if got := TonemapReinhard(tt.value, tt.whitePoint); got != tt.want {
			t.Errorf("%s: TonemapReinhard(%v, %v) = %v, want %v", tt.name, tt.value, tt.whitePoint, got, tt.want)
		}
	}
}

func TestNeedsTonemap(t *testing.T) {
	if NeedsTonemap(1.0) {
		t.Error("SDR 范围不应触发 tonemap")
	}
	if NeedsTonemap(4.0) != true {
		t.Error("真 HDR 应触发 tonemap")
	}
}

func TestFloatToByte(t *testing.T) {
	if FloatToByte(0.0) != 0 || FloatToByte(1.0) != 255 || FloatToByte(9.9) != 255 {
		t.Error("clamp 边界错误")
	}
	if FloatToByte(0.5) != 128 { // 0.5*255+0.5=128.0 → 128
		t.Error("半值进位错误")
	}
}
