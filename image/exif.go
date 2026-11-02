// EXIF Orientation（标签 0x0112）读取——图片界的"可烤旋转"。
// 手机 JPEG/TIFF 的像素按传感器方向存储、观看器按标签转——与视频 display matrix 同构。
// 零依赖最小解析：JPEG 扫 APP1 段 / TIFF 直接走 IFD0，只取 0x0112 一个 SHORT 值。
package image

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
)

// EXIF_TAG_ORIENTATION IFD0 中的方向标签
const EXIF_TAG_ORIENTATION = 0x0112

// ReadOrientation 读 EXIF Orientation（1-8；0 = 无标签=按原样显示）。
// 仅 JPEG（APP1 Exif）与 TIFF（IFD0）——PNG/WEBP/BMP/GIF 无此惯例。
func ReadOrientation(path string) int {
	ext := strings.ToLower(filepath.Ext(path))
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	switch ext {
	case ".jpg", ".jpeg", ".jfif":
		return readJPEGOrientation(file)
	case ".tif", ".tiff":
		return readTIFFOrientation(file)
	default:
		return 0
	}
}

// readJPEGOrientation 扫 JPEG 段：FFD8 后逐段找 APP1 "Exif\0\0"，内层按 TIFF 解析
func readJPEGOrientation(file *os.File) int {
	// SOI 校验
	soi := make([]byte, 2)
	if readFull(file, soi) != nil || soi[0] != 0xFF || soi[1] != 0xD8 {
		return 0
	}
	for {
		marker := make([]byte, 2)
		if readFull(file, marker) != nil || marker[0] != 0xFF {
			return 0
		}
		// 填充字节（0xFF 连续）跳过；SOS(0xDA) 起为压缩数据——元数据段已结束
		if marker[1] == 0xFF {
			continue
		}
		if marker[1] == 0xDA {
			return 0
		}
		lengthBytes := make([]byte, 2)
		if readFull(file, lengthBytes) != nil {
			return 0
		}
		segmentLength := int(lengthBytes[0])<<8 | int(lengthBytes[1])
		if segmentLength < 2 {
			return 0
		}
		payload := make([]byte, segmentLength-2)
		if readFull(file, payload) != nil {
			return 0
		}
		// APP1 + "Exif\0\0" 头
		if marker[1] == 0xE1 && len(payload) > 6 && string(payload[:6]) == "Exif\x00\x00" {
			return parseTIFFOrientation(payload[6:])
		}
	}
}

// readTIFFOrientation TIFF 文件本体即 TIFF 结构，IFD0 直接走
func readTIFFOrientation(file *os.File) int {
	tiff := make([]byte, 8)
	if readFull(file, tiff) != nil {
		return 0
	}
	ifOdsOffset := parseTIFFHeader(tiff)
	if ifOdsOffset <= 0 {
		return 0
	}
	body := make([]byte, ifOdsOffset)
	if _, err := file.Seek(0, 0); err != nil {
		return 0
	}
	if readFull(file, body) != nil {
		return 0
	}
	return findOrientationInIFD(body, ifOdsOffset)
}

// parseTIFFHeader 字节序 + IFD0 偏移（非法返回 0）
func parseTIFFHeader(header []byte) int {
	if len(header) < 8 {
		return 0
	}
	if string(header[:4]) == "II*\x00" || string(header[:4]) == "MM\x00*" {
		return int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
	}
	return 0
}

// parseTIFFOrientation TIFF 字节流（自头部起）中找 IFD0 的 orientation
func parseTIFFOrientation(tiff []byte) int {
	ifdOffset := parseTIFFHeader(tiff)
	if ifdOffset <= 0 || ifdOffset >= len(tiff) {
		return 0
	}
	return findOrientationInIFD(tiff, ifdOffset)
}

// findOrientationInIFD 走一张 IFD 找 0x0112（SHORT 内联值）
func findOrientationInIFD(tiff []byte, ifdOffset int) int {
	byteOrder := binary.ByteOrder(binary.LittleEndian)
	if len(tiff) >= 2 && string(tiff[:2]) == "MM" {
		byteOrder = binary.BigEndian
	}
	if ifdOffset+2 > len(tiff) {
		return 0
	}
	entryCount := int(byteOrder.Uint16(tiff[ifdOffset : ifdOffset+2]))
	for entryIdx := 0; entryIdx < entryCount; entryIdx++ {
		entryStart := ifdOffset + 2 + entryIdx*12
		if entryStart+12 > len(tiff) {
			return 0
		}
		entry := tiff[entryStart : entryStart+12]
		tag := byteOrder.Uint16(entry[0:2])
		if tag != EXIF_TAG_ORIENTATION {
			continue
		}
		typeCode := byteOrder.Uint16(entry[2:4])
		if typeCode != 3 { // 非 SHORT——异常当无标签
			return 0
		}
		orientation := int(byteOrder.Uint16(entry[8:10]))
		if orientation >= 1 && orientation <= 8 {
			return orientation
		}
		return 0
	}
	return 0
}

// readFull 读满或报错
func readFull(file *os.File, buf []byte) error {
	total := 0
	for total < len(buf) {
		n, err := file.Read(buf[total:])
		total += n
		if err != nil {
			return err
		}
	}
	return nil
}
