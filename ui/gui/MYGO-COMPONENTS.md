# mygo v0.10.0 组件清单（ui/gui 工作前必查）

> 教训（2026-11-21，用户震怒）：清单拖拽排序手搓了 8 轮提交（d5f19fa→325e283）
> 仍变高行错位，而框架 `ui.List`+`ListState.Reorder` 开箱即得。**动手造任何
> UI 行为之前，先查本清单**——查不到再来手搓，并把结论记回本文件。

## 组件级（ui 包函数，38 组件族）

| 类别 | 组件 | 备注 |
|---|---|---|
| 布局 | Row / Column / Box / Spacer / Grid / Split(Vertical)(Percent) / Divider / AspectRatio | Split=分栏可拖分隔条 |
| 文本 | Text / Textf / RichText / EditableText / Link / Label | RichText 富文本 |
| 按钮 | Button / ButtonBase / PrimaryButton / BackButton / ForwardButton / SplitButton / PrimarySplitButton / MenuButton / PopoverMenuButton | ButtonBase=裸按钮（全观感自控） |
| 输入 | TextInput(Base) / TextArea(Base/Buffer) / NumberInput / DecimalInput / DateInput / TimeInput / SearchField / Password( TextInput.Password) / Autocomplete / TokenField / Combobox(Base) | **TextArea 无 Lines 缺省≥3 行**，一行高须显式 `.Lines(1,3)` |
| 选择 | Checkbox(Base/State) / Radio(Group/Base) / Switch(Base) / Toggle(Group/Base) / Segmented(Base) / Select(Base) / Slider(Base) / RangeSlider / StepSlider / Rating / ColorPicker / ColorWell / Calendar / Tree(Base/Item) | SelectBase=弹层机件全可控 |
| 列表表格 | **List**（ListState）/ Table / OutlineTable / GridView(GridState) / Outline | **ListState.Reorder=拖拽排序**；Key=行身份；MoveAnimation=换位滑入；Header=分组吸顶；FollowEnd=聊天吸底；DragRows=拖出行；DragSelect=框选 |
| 容器交互 | Tabs(Base) / Accordion(Item/Base) / Collapsible(Base) / Sidebar(Section/Item) / Toolbar / Breadcrumbs / Stepper / Field / Fieldset / Form / FindBar / Modal / Overlay / Popover(Base) / Dialog(Base) / AlertDialog / MenuBase / ContextMenuBase / Badge / Avatar / Progress / Meter / Spinner / Tooltip(Base) / Toast(Viewport)Base / Video / WebView / NativeView / Scroll / ScrollBoth / ScrollHorizontal | Sidebar 选中态=灰 pill（本设计弃用改 navItem，**有意识弃用≠不知道**） |
| 泛型 | Drop[T]/DropData/DragOver[T]/Local[T]/Toggle/MenuBase | Drag/Draggable=自由拖拽位移；Drop=接收外部拖值 |

## mygo 包（应用级）

App.Run/Quit · Window(New/Flyout/NewNative) · Dialog(Message/Open/…) · Clipboard · App.Dock(SetIcon/Badge/Menu) · Notifications? · Shortcuts · Recorder/Tester（UI 测试回放：NewTester 可合成点击！）

## Element 方法速查（343 个，高频分组）

- **盒模型**：Width/Height(+Percent) Min/Max Padding(X/Y)(Start/End) Margin* Gap(X/Y) Radius Border(+Color/Width(1-4 参 CSS)/Start/End/Style) Background Shadow InsetShadow Clip(ClipInset/Percent) ZIndex Opacity Fill(Width/Height/Equally) Grow Shrink Basis
- **排版**：Font(Size/Weight/Features) Bold Italic Lines(最少,最多) MaxLines FixedLineHeight LetterSpacing LineHeight Ellipsis NoWrap Balance Align(Items/Self/Content/Justify) Justify(Items/Self) Center Start End
- **交互状态**：Hovered Pressed(Inside/Outside/Button) Clicked DoubleClicked RightClicked LongPressed ClickModifiers PressCanceled Focused/FocusWithin/Focusable Focus KeepFocus Blur Cursor HitTest PassThrough IgnoreInput
- **拖放**：Draggable Dragged Dragging DragOffset DragVelocity DragEnded DragMissed Drag DragData DragWindow FileDragOver DroppedFiles Drop[T] DragOver[T]
- **动画**：Animate(key,target,dur) AnimateColor AnimatePose AnimateTo/With Keyframes Transition Arrived（ReduceMotion 自动直跳）
- **变换**：Translate Rotate(RotateX/Y) Scale Perspective BackfaceHidden TransformOrigin
- **弹层**：Popover(Menu) Attach(To/EdgeMargin/AttachedAnchors/Origin) Modal Overlay KeepOpen Dismissed
- **列表行**：RowClicked RowDoubleClicked RowWidth RowIndex RowSpan RowTracks RowCount
- **窗口**：LocalToWindow Bounds LayoutBounds PositionInSet MaximizeButton DragWindow
- **其他**：Tooltip 经 TooltipBase；ContextMenu(func(m *Menu))；ScrollIntoView/InView(Amount/Margin)；ID/Role/LabelledBy/DescribedBy（无障碍）；ObserveInput/OnTextKey/SetTextSelection/SelectedTexts（输入观测）

## 本 GUI 使用审计（2026-11-21）

| 手搓处 | 框架对应 | 裁决 |
|---|---|---|
| navItem/navSectionHeader 侧栏 | Sidebar/SidebarItem | **有意弃用**（选中态灰 pill 违设计定案），保留 |
| settingRow 设置行 | Field/Form | 当前两列卡是设计定案，暂保留；若做设置页再评估 |
| ~~清单拖拽排序~~ | List.ListState.Reorder | **已换**（0e9eb02）——教训本体 |
| heroPane 空态 | 无对应 | 保留 |
| 后缀下拉自绘 | SelectBase 机件 | 已用官方机件自定观感（合规） |
| 减号/尺寸槽动画 | Animate | 已用官方（合规） |
| 任务卡片 | List/Table? | 暂留（条数少），多起来再 List |
| 进度显示 | Progress/Spinner | **候选**：压缩中任务行可上 Progress 条 |
| pwCharsetSelect 字符集多选（随机生成页） | MenuBase + CheckItem + KeepOpen + SubmenuItem/Submenu | 已用官方机件自定观感（合规，照 PopoverMenuButton 主题配方）。**查证结论（2026-11-23）**：mygo v0.10.0（及当日主干）没有独立的 MultiSelect；带主题的 `*ui.Menu`（MenuButton/PopoverMenuButton）选中一项即关菜单，多选须走 MenuBase+KeepOpen |
| convertCTARow 转换类主按钮 | PrimaryButton | **已换**（2026-11-23）——此前 ButtonBase 自绘缺按下态；框架自带 AccentHover/AccentPressed；注意覆盖 Background 会关掉框架的悬停/按下面（ui/widgets.go:184），只在禁用态覆盖 |
| 哈希清单 / 图片压缩清单 / 刻录清单 | List + inputListRow | **已换**（2026-11-23）——五张清单同一个小件（刻录页此前是旧版行的整段复制） |
| pressFeedback / leadBrowseButton / pickButton / copyButton 反馈小件 | ButtonBase + Hovered/Pressed | 机件合规：反馈语汇的唯一定义点（悬停 SurfaceHover / 按下 pressedShade），页面代码不得再内联状态判断。pickButton 自带 4px 右内缩（`Margin`）——调用处不要再写 Margin |
| 设置页仓库链接 | Link | 框架 Link 只有悬停下划线、无按下态——按下字色（AccentPressed）自补 |
| 清单行 / 卡片的路径 | Text（换行） | pathCell：整段纯文字完整显示——**不截断、不分色**（2026-11-23 用户否决了 RichText+TruncateStart 的省略方案：过度设计） |
| 刻录引导行 | Row.Wrap + Basis | 机件合规：句子 `Basis(下限).Grow(1)` 先收缩，放不下才把按钮换行 |
| 行内校验红字 | Form / Field | 当前两列行是设计定案，自绘 settingControl（控件列 + 红字）；校验规则在 validate.go 纯函数 |

## 规矩（新增，已同步 AGENTS.md）

1. 任何交互行为动手前：先查本清单 + `grep -n "func.*Xxx" $MYGO/ui/api_gen.go`。
2. 用了组件但深度定制（ButtonBase 自绘）→ 注明「机件合规」；纯手搓 → 必须先在本文件记一行弃用理由。
3. 组件行为存疑 → 直接读 `examples/`（gallery 是全集示例）。
4. **新增可点元素必须在 `feedback_test.go` 的 `feedbackTargets` 登记**（悬停/按下/手型三态断言，全页面 75 个目标常驻验收）；真有豁免理由的标 `rowOnly` 并写明。
5. **页面函数假定清单非空**（空态由 pane 判 `heroPage()` 走 hero）：无头测试渲染页面要经 `heroAwarePage`，直接渲染空清单会读 `inputs[0]` 越界。**清单增删在建帧结束后才落地**（压缩台 / PDF 合并的参数区同帧还要读首输入）。
