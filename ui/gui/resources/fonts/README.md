MiSans 可变字体（MiSans VF）
=============================

来源：小米 MiSans 官方字库（https://hyperos.mi.com/font）
许可：小米 MiSans 字体许可——免费商用，允许随应用内嵌分发（2026-11 官方声明）。
文件：MiSans-W450.ttf.gz（正文默认——VF 经 fontTools varLib.instancer 钉死
wght=450 的静态单实例，OS/2.usWeightClass=450；2026-11-23。不用 VF 裸注册做
正文：fvar 缺省实例 330（Light）偏细。字重裁定链：500 Medium 太粗（用户）→400
Regular 回退→定格 450（用户，介于 Regular 与 Medium 之间））
文件：MiSans-VF.ttf.gz（完整可变字体 wght 150-700——仅 Bold 标题族「MiSans VF」
用，显式 FontWeight 需要字重插值时落在它上面）

zpack GUI 全平台默认字体：经 mygo ui.RegisterFont 注册为 "MiSans" 族，
ui.Theme.Font 指向该族——三平台（Windows dwrite / macOS CoreText / Linux pango）统一渲染。
显式 Font("monospace") 调用点（路径/代码展示）不受影响。

⚠️ 派生定制（2026-11-23）：MiSans VF 的 wght 轴是**非标准阶梯**（150–700，默认 330；
Regular=330 / Medium=380 / Demibold=450 / Semibold=520 / Bold=630 / Heavy=700）——
CSS 默认字重请求 400 最近命中 380（Medium），视觉偏细。已用 fontTools 把 Medium
实例坐标 380→450（仅 fvar 元数据一处，字形数据零改动），使默认请求落点抬到
Demibold 450；显式 FontWeight(500/600/700) 命中不变。嵌入文件为本派生版。

JetBrains Mono 可变字体（JetBrainsMono-VF.ttf）
================================================

来源：JetBrains 官方开源（https://github.com/JetBrains/JetBrainsMono，v2.304）
许可：OFL 1.1（SIL Open Font License，随库 JetBrainsMono-OFL.txt）——免费商用，允许随应用内嵌分发。
用途：英文/数字展示位（尺寸/哈希/百分比/随机密码等等宽场景）首选族 fontMonoFamily，
      逗号列表次选系统 monospace 兜底；wght 100–800 标准 CSS 阶梯，无需 MiSans 式字重手术。
