# realEnvTest —— 真实环境测试数据

`realEnvTest/`（仓库根，gitignore）= 用算法生成的真实体感数据。verify 门禁测确定性小样本；
这里测**几 GB 随机数据的进度条、真实视频/音频走转换压缩、加密分卷归档走解压台**。
全部确定性生成（固定种子，可复现），零下载；随时 `--clean` 推平重铸。

```bash
go run ./scripts/genenv          # 标准集 ≈3.4GB（3×1G 随机 + 语料 + 媒体 + 归档，~10s）
go run ./scripts/genenv --fast   # 小件冒烟 <300MB（媒体/归档/大文件跳过）
go run ./scripts/genenv --full   # 标准集 + rand-5g.bin（长进度条观测）
go run ./scripts/genenv --clean  # 删除整树
```

幂等：已存在的件跳过；媒体族依赖 `bin/zpack` 完整版（进程内 ffmpeg 直调），缺失时如实跳过。

## 目录 → 测试场景对照

| 路径 | 内容 | 拖到哪测 |
|---|---|---|
| `corpus/rand-{1,2,3}g.bin` | 1GB×3 不可压随机（固定种子 0x7A706163） | 压缩台观测进度条/速率/剩余时间；归档浏览看比值 |
| `corpus/rand-5g.bin`（--full） | 5GB 单文件 | 长任务进度条、取消压缩、处理后清理 |
| `corpus/text-200m.txt` | 可压文本（比值明显） | 压缩台对比随机数据的比值差 |
| `corpus/项目资料/` | 混合目录树：中文名/空格名/三层嵌套/大小文件 | 压缩台拖目录、目录体积异步、拖拽排序 |
| `media/video-720p-60s.mp4` | testsrc2+x264+aac 60s | 视频转换（remux/转码）、视频压缩、media 信息 |
| `media/video-rotated.mp4` | 带 display-matrix 90° | 旋转族、bake、info 的 rotation/display 字段 |
| `media/audio-tone-320k.mp3` | 440Hz 320k | 音频转换/压缩（码率减半链） |
| `media/photo-6000x4000.jpg` | 6000×4000 | 图片压缩（size-first 二分缩放）、图片转换预览 |
| `media/photo-transparent.png` | 全透明 RGBA | alpha 保真转换 |
| `archives/机密归档.zip` | 密码 test1234（WinZip AES-256） | 解压台加密检测+密码行；归档浏览「已加密」 |
| `archives/项目备份.zip.001-002` | 2GB 双卷 | 分卷自动续卷（.001 直拖即整档）；分卷刻录 |
| `archives/文档集.tar.gz` | tar.gz | 解压台/归档浏览格式识别 |
| `out/` | 空目录 | GUI 输出落点（观察产物，不污染别处） |
