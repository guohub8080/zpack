package main

// 刻录页的介质状态规则（纯函数，规则可单测）。status 是 burn devices 输出里的 status：
// Windows 引擎报 blank / appendable / full / no-media / damaged / erase-required / write-protected / unsupported；
// macOS 只报 no-media（有盘时为空）；Linux 是 xorriso 状态短语归一后的值，认不出的原样透传。
// 所以只有明确认识的状态才下结论，其余一律不拦、不起名提示也不改——交给引擎裁决，避免误拦 mac/Linux。

// burnBlockReason 选中刻录机的介质挡住刻录的原因（可刻返回空串）。erase=勾了「先擦空再烧」：
// 可擦写盘的异常状态擦除即恢复，不再拦截
func burnBlockReason(status string, erase bool) string {
	switch status {
	case "full":
		return "光盘已封盘"
	case "no-media":
		return "未放入光盘"
	case "damaged":
		if erase {
			return ""
		}
		return "光盘状态异常"
	case "erase-required":
		if erase {
			return ""
		}
		return "光盘需先擦除"
	case "write-protected":
		return "光盘被写保护"
	case "unsupported":
		return "不支持这张光盘"
	}
	return ""
}

// burnMediaSuffix 设备下拉里介质状态的括注（认不出的状态不加括注）
func burnMediaSuffix(status string) string {
	switch status {
	case "blank":
		return "（空白）"
	case "appendable":
		return "（可追加）"
	case "full":
		return "（已满）"
	case "no-media":
		return "（无介质）"
	case "damaged":
		return "（状态异常）"
	case "erase-required":
		return "（需先擦除）"
	case "write-protected":
		return "（写保护）"
	case "unsupported":
		return "（不支持）"
	}
	return ""
}
