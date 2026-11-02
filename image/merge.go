// 图片拼接：多图纵向（长图）/横向拼接。基准图的对齐轴尺寸为准，
// 其余图等比缩放对齐后拼接——同 media merge 的 --reference 语义。
package image

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// MERGE_DIRECTION_VERTICAL 纵向拼接（长图——上下堆叠，宽对齐）
const MERGE_DIRECTION_VERTICAL = "vertical"

// MERGE_DIRECTION_HORIZONTAL 横向拼接（左右排开，高对齐）
const MERGE_DIRECTION_HORIZONTAL = "horizontal"

// MergeResult 拼接结果（信封 data 载荷）
type MergeResult struct {
	Reference string   `json:"reference"`
	Inputs    []string `json:"inputs"`
	Output    string   `json:"output"`
	Direction string   `json:"direction"`
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Format    string   `json:"format"`
	Count     int      `json:"image_count"`
	SizeBytes int64    `json:"size_bytes"`
}

// Merge 拼接多图：vertical=上下堆叠（宽=基准宽，其余等比缩放）/ horizontal=左右排开（高=基准高）。
// referencePath 空 = 首图为基准；基准必须在输入列表中。
func Merge(inputs []string, output, referencePath, direction string, quality int) (*MergeResult, error) {
	if len(inputs) < 2 {
		return nil, fmt.Errorf("至少需要 2 张图片")
	}
	if direction == "" {
		direction = MERGE_DIRECTION_VERTICAL
	}
	if direction != MERGE_DIRECTION_VERTICAL && direction != MERGE_DIRECTION_HORIZONTAL {
		return nil, fmt.Errorf("拼接方向需 vertical 或 horizontal，收到: %s", direction)
	}
	reference := inputs[0]
	if referencePath != "" {
		found := false
		for _, inputPath := range inputs {
			if inputPath == referencePath {
				reference = referencePath
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("--reference %s 不在输入列表中", referencePath)
		}
	}

	// 全量解码（拼接需要随机访问每张图的尺寸；图片内存占用远小于视频帧）
	decoded := make([]image.Image, len(inputs))
	for inputIdx, inputPath := range inputs {
		img, _, readErr := ReadImageOriented(inputPath)
		if readErr != nil {
			return nil, fmt.Errorf("读取图片 %d 失败: %w", inputIdx+1, readErr)
		}
		decoded[inputIdx] = img
	}
	refIdx := 0
	for inputIdx, inputPath := range inputs {
		if inputPath == reference {
			refIdx = inputIdx
			break
		}
	}

	// 目标尺寸：对齐轴取基准，另一轴累加
	bounds := make([]image.Rectangle, len(inputs))
	dstWidth, dstHeight := 0, 0
	if direction == MERGE_DIRECTION_VERTICAL {
		dstWidth = decoded[refIdx].Bounds().Dx()
		for inputIdx, img := range decoded {
			scaledHeight := scaleOnAxis(img.Bounds().Dx(), img.Bounds().Dy(), dstWidth)
			bounds[inputIdx] = image.Rect(0, dstHeight, dstWidth, dstHeight+scaledHeight)
			dstHeight += scaledHeight
		}
	} else {
		dstHeight = decoded[refIdx].Bounds().Dy()
		for inputIdx, img := range decoded {
			scaledWidth := scaleOnAxis(img.Bounds().Dy(), img.Bounds().Dx(), dstHeight)
			bounds[inputIdx] = image.Rect(dstWidth, 0, dstWidth+scaledWidth, dstHeight)
			dstWidth += scaledWidth
		}
	}

	// 逐图缩放落位：1:1 直拷（bit-exact），否则 CatmullRom 高质量缩放进子区域
	canvas := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	for inputIdx, img := range decoded {
		dstRect := bounds[inputIdx]
		srcBounds := img.Bounds()
		if dstRect.Dx() == srcBounds.Dx() && dstRect.Dy() == srcBounds.Dy() {
			xdraw.Draw(canvas, dstRect, img, srcBounds.Min, xdraw.Src)
		} else {
			xdraw.CatmullRom.Scale(canvas, dstRect, img, srcBounds, xdraw.Over, nil)
		}
	}

	outputFormat := strings.ToLower(strings.TrimPrefix(filepath.Ext(output), "."))
	if outputFormat == "" {
		outputFormat = "png" // 长图缺省 PNG——无损且支持透明
	}
	if writeErr := WriteImage(output, canvas, outputFormat, quality); writeErr != nil {
		return nil, fmt.Errorf("写入拼接图失败: %w", writeErr)
	}

	outputInfo, _ := os.Stat(output)
	absInputs := make([]string, len(inputs))
	for i, p := range inputs {
		absInputs[i], _ = filepath.Abs(p)
	}
	absOutput, _ := filepath.Abs(output)
	absRef, _ := filepath.Abs(reference)
	return &MergeResult{
		Reference: absRef, Inputs: absInputs, Output: absOutput,
		Direction: direction, Width: dstWidth, Height: dstHeight,
		Format: outputFormat, Count: len(inputs), SizeBytes: outputInfo.Size(),
	}, nil
}

// scaleOnAxis 等比缩放后的目标轴尺寸（源宽 sourceMain → 源高 sourceCross 的比例，对齐到 targetMain）
func scaleOnAxis(sourceMain, sourceCross, targetMain int) int {
	if sourceMain <= 0 {
		return 0
	}
	return int(float64(sourceCross)*float64(targetMain)/float64(sourceMain) + 0.5)
}
