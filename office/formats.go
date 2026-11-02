// Office 格式矩阵（双构建共享）
package office

// LEGACY_OFFICE_INPUTS 旧格式输入矩阵：扩展名 → office_oxide 格式名
// （WPS 现代文件与 MS 旧格式同构：.wps=doc / .et=xls / .dps=ppt）
var LEGACY_OFFICE_INPUTS = map[string]string{
	".doc": "doc", ".wps": "doc",
	".xls": "xls", ".et": "xls",
	".ppt": "ppt", ".dps": "ppt",
}

// OFFICE_FAMILY 族匹配：输入扩展名 → 合法输出扩展名（doc 族只出 docx…跨族拒绝）
var OFFICE_FAMILY = map[string]string{
	".doc": ".docx", ".wps": ".docx",
	".xls": ".xlsx", ".et": ".xlsx",
	".ppt": ".pptx", ".dps": ".pptx",
}
