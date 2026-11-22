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

## 规矩（新增，已同步 AGENTS.md）

1. 任何交互行为动手前：先查本清单 + `grep -n "func.*Xxx" $MYGO/ui/api_gen.go`。
2. 用了组件但深度定制（ButtonBase 自绘）→ 注明「机件合规」；纯手搓 → 必须先在本文件记一行弃用理由。
3. 组件行为存疑 → 直接读 `examples/`（gallery 是全集示例）。
