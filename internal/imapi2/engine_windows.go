//go:build windows

// 引擎编排：设备枚举、介质查询、镜像构建（目录 / 散文件 → 光盘镜像）、写入、擦除。
package imapi2

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

// CLSID（注册表 HKCR\CLSID，与 SDK 的 coclass 定义逐个核对；本机无脚本封装层的 ProgID，只能按 CLSID 创建）
const (
	CLSID_DISC_MASTER       = "{2735412E-7F64-5B0F-8F00-5D77AFBE261E}" // imapi2.dll   Disc Master：设备枚举
	CLSID_DISC_RECORDER     = "{2735412D-7F64-5B0F-8F00-5D77AFBE261E}" // imapi2.dll   CD DVD Device Object：刻录机
	CLSID_FORMAT2_DATA      = "{2735412A-7F64-5B0F-8F00-5D77AFBE261E}" // imapi2.dll   Standard Data Writer：数据写入
	CLSID_FORMAT2_ERASE     = "{2735412B-7F64-5B0F-8F00-5D77AFBE261E}" // imapi2.dll   Media Erase：擦除
	CLSID_FILE_SYSTEM_IMAGE = "{2C941FC5-975B-59BE-A960-9A2A262853A5}" // imapi2fs.dll FileSystemImage：目录→镜像
)

// CLIENT_NAME 引擎记录的客户端名（刻录机被别的程序占用时，占用方会看到它）
const CLIENT_NAME = "zpack"

// FsiFileSystems 光盘文件系统（SDK imapi2fs.idl）
const (
	FSI_FILESYSTEM_ISO9660 int32 = 1 // 兼容性兜底（短文件名）
	FSI_FILESYSTEM_JOLIET  int32 = 2 // 长文件名 / 非 ASCII 文件名
	FSI_FILESYSTEM_UDF     int32 = 4 // 现代系统的首选

	// FSI_FILESYSTEMS_ALL 三种全建——与资源管理器「母盘式」刻录一致
	FSI_FILESYSTEMS_ALL int32 = FSI_FILESYSTEM_ISO9660 | FSI_FILESYSTEM_JOLIET | FSI_FILESYSTEM_UDF
)

// HEARTBEAT_INTERVAL 长操作（写入 / 擦除）期间的心跳间隔
const HEARTBEAT_INTERVAL = 5 * time.Second

// 文件打开模式 STGM（objbase.h，只取用到两个）
const (
	stgmRead          = 0x00000000 // 只读
	stgmShareDenyNone = 0x00000040 // 不排斥其他读者
)

// procSHCreateStreamOnFileEx 系统自带的「文件 → IStream」工厂（shlwapi.dll）——不手写 IStream
var procSHCreateStreamOnFileEx = syscall.NewLazyDLL("shlwapi.dll").NewProc("SHCreateStreamOnFileEx")

// Recorder 刻录机
type Recorder struct {
	Index      int    // 设备序号（--device 用）
	UniqueID   string // 引擎唯一标识
	Vendor     string
	Product    string
	Revision   string
	VolumePath string // 盘符路径（如 E:\）；无挂载点时为空
}

// Device 刻录机及其当前介质（burn devices 的一条）
type Device struct {
	Recorder
	Media      Media  // 无介质时 Type=MEDIA_TYPE_UNKNOWN
	MediaError string // 介质查询失败的原因；成功为空
}

// recorderCount 系统里的刻录机数量
func recorderCount() (int, error) {
	master, err := createObject(CLSID_DISC_MASTER)
	if err != nil {
		return 0, err
	}
	defer master.Release()
	count, err := getInt(master, "Count", "刻录机数量")
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// openRecorder 打开第 index 台刻录机（调用方 Release）
func openRecorder(index int) (*ole.IDispatch, error) {
	master, err := createObject(CLSID_DISC_MASTER)
	if err != nil {
		return nil, err
	}
	defer master.Release()
	uniqueIDValue, err := oleutil.GetProperty(master, "Item", index) // SDK 把 Item 定义为带参属性
	if err != nil {
		return nil, wrapCall("读取刻录机标识", err)
	}
	uniqueID := uniqueIDValue.ToString()
	_ = uniqueIDValue.Clear()
	recorder, err := createObject(CLSID_DISC_RECORDER)
	if err != nil {
		return nil, err
	}
	if err := callVoid(recorder, "InitializeDiscRecorder", "打开刻录机", uniqueID); err != nil {
		recorder.Release()
		return nil, err
	}
	return recorder, nil
}

// ListDevices 枚举全部刻录机并读取各自的介质（无刻录机=空列表；单台的介质查询失败不阻塞枚举）
func ListDevices() ([]Device, error) {
	count, err := recorderCount()
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, count)
	for index := 0; index < count; index++ {
		device := Device{Recorder: Recorder{Index: index}}
		writer, openErr := OpenDataWriter(index)
		if openErr != nil {
			device.MediaError = openErr.Error()
			devices = append(devices, device)
			continue
		}
		device.Recorder = writer.Recorder()
		media, mediaErr := writer.Media()
		device.Media = media
		if mediaErr != nil {
			device.MediaError = mediaErr.Error()
		}
		writer.Close()
		devices = append(devices, device)
	}
	return devices, nil
}

// DataWriter 数据写入器：绑定一台刻录机，负责介质查询 / 写入 / 擦除
type DataWriter struct {
	writer   *ole.IDispatch // IDiscFormat2Data
	recorder *ole.IDispatch // IDiscRecorder2
	info     Recorder
}

// OpenDataWriter 打开第 index 台刻录机并绑定数据写入器
func OpenDataWriter(index int) (*DataWriter, error) {
	count, err := recorderCount()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= count {
		return nil, &RecorderIndexError{Index: index, Count: count}
	}
	recorder, err := openRecorder(index)
	if err != nil {
		return nil, err
	}
	writer, err := createObject(CLSID_FORMAT2_DATA)
	if err != nil {
		recorder.Release()
		return nil, err
	}
	bound := &DataWriter{writer: writer, recorder: recorder, info: Recorder{Index: index}}
	// ForceOverwrite：可覆写介质（DVD±RW / BD-RE）非空白时写入的前提；一次写入介质上无此语义
	err = putValue(writer, "Recorder", "刻录机", recorder)
	if err == nil {
		err = putValue(writer, "ForceOverwrite", "覆写许可", true)
	}
	if err == nil {
		err = putValue(writer, "ClientName", "客户端名", CLIENT_NAME)
	}
	if err != nil {
		bound.Close()
		return nil, err
	}
	bound.info.UniqueID, _ = getString(recorder, "ActiveDiscRecorder", "刻录机标识")
	bound.info.Vendor, _ = getString(recorder, "VendorId", "厂商")
	bound.info.Product, _ = getString(recorder, "ProductId", "型号")
	bound.info.Revision, _ = getString(recorder, "ProductRevision", "固件版本")
	if paths, pathErr := readArray(recorder, "VolumePathNames", "盘符"); pathErr == nil && len(paths) > 0 {
		bound.info.VolumePath, _ = paths[0].(string)
	}
	return bound, nil
}

// Recorder 刻录机信息
func (writer *DataWriter) Recorder() Recorder {
	return writer.info
}

// Close 释放写入器与刻录机
func (writer *DataWriter) Close() {
	if writer == nil {
		return
	}
	if writer.writer != nil {
		writer.writer.Release()
		writer.writer = nil
	}
	if writer.recorder != nil {
		writer.recorder.Release()
		writer.recorder = nil
	}
}

// discUnreadable 让引擎读一遍盘上的文件系统：一个受支持的数据文件系统都读不出才算「读不出」。
// 读得到但上面没有东西（IMAPI_E_EMPTY_DISC）、或有盘但没有本引擎认识的数据文件系统（音频 CD 之类，
// IMAPI_E_NO_SUPPORTED_FILE_SYSTEM）都不算——那是一张读得出的只读盘。引擎对象建不出来时不下结论
func discUnreadable(recorder *ole.IDispatch) bool {
	image, err := createObject(CLSID_FILE_SYSTEM_IMAGE)
	if err != nil {
		return false
	}
	defer image.Release()
	identifyErr := callVoid(image, "IdentifyFileSystemsOnDisc", "识别盘上文件系统", recorder)
	if identifyErr == nil || IsSCode(identifyErr, SCODE_EMPTY_DISC) || IsSCode(identifyErr, SCODE_NO_SUPPORTED_FILE_SYSTEM) {
		return false
	}
	return true
}

// Media 查询当前介质（无介质时返回 Type=MEDIA_TYPE_UNKNOWN 的零值信息；认不出的盘 Unrecognized=true）
func (writer *DataWriter) Media() (Media, error) {
	media := Media{PreviousSession: -1}
	mediaType, err := getInt(writer.writer, "CurrentPhysicalMediaType", "介质类型")
	if IsSCode(err, SCODE_MEDIA_NO_MEDIA) {
		return media, nil // 没放盘：引擎以错误码报告，归一成「无介质」
	}
	if IsSCode(err, SCODE_MEDIA_INCOMPATIBLE) {
		// 有盘、光驱认不出它的身份：同样是错误码报告，归一成「认不出」而不是当成查询失败
		media.Unrecognized = true
		return media, nil
	}
	if err != nil {
		return media, err
	}
	media.Type = mediaType
	if !media.Present() {
		return media, nil
	}
	if media.State, err = getInt(writer.writer, "CurrentMediaStatus", "介质状态"); err != nil {
		return media, err
	}
	// 「只读类型 + 写保护位」有两种可能：真是压盘 / 已封盘，或光驱读不出内容的盘——再读一次文件系统才能分开
	if media.State&MEDIA_STATE_WRITE_PROTECTED != 0 && IsRomMediaType(media.Type) && discUnreadable(writer.recorder) {
		media.Unrecognized = true
	}
	// 其余为辅助信息：个别介质 / 刻录机不支持某项查询时留空，不算失败
	media.PhysicallyBlank, _ = getBool(writer.writer, "MediaPhysicallyBlank", "空白判定")
	media.HeuristicallyBlank, _ = getBool(writer.writer, "MediaHeuristicallyBlank", "空白判定")
	media.TotalSectors, _ = getInt(writer.writer, "TotalSectorsOnMedia", "介质容量")
	media.FreeSectors, _ = getInt(writer.writer, "FreeSectorsOnMedia", "剩余空间")
	if previous, previousErr := getInt(writer.writer, "StartAddressOfPreviousSession", "上一区段"); previousErr == nil {
		media.PreviousSession = previous
	}
	if speeds, speedErr := readArray(writer.writer, "SupportedWriteSpeeds", "写入速度"); speedErr == nil {
		for _, element := range speeds {
			if kbps, ok := element.(int32); ok {
				media.WriteSpeeds = append(media.WriteSpeeds, kbps)
			}
		}
	}
	media.SupportedByWriter, _ = callBool(writer.writer, "IsCurrentMediaSupported", "介质支持判定", writer.recorder)
	return media, nil
}

// SetSpeed 设置写入速度（单位：扇区 / 秒）
func (writer *DataWriter) SetSpeed(sectorsPerSecond int32) error {
	return callVoid(writer.writer, "SetWriteSpeed", "设置写入速度", sectorsPerSecond, false)
}

// SetCloseMedia 写完后是否封盘（封盘后不能再追加区段）
func (writer *DataWriter) SetCloseMedia(close bool) error {
	return putValue(writer.writer, "ForceMediaToBeClosed", "封盘设置", close)
}

// Write 把流写入光盘，阻塞至完成。heartbeat 可为 nil；非 nil 时每 HEARTBEAT_INTERVAL 收到一次已用时长。
// 百分比进度只由引擎的事件回调提供（需自建 COM 事件接收器），暂未接入
func (writer *DataWriter) Write(stream *Stream, heartbeat func(elapsed time.Duration)) error {
	stop := startHeartbeat(heartbeat)
	defer close(stop)
	result, err := invokeRaw(writer.writer, "Write", ole.DISPATCH_METHOD, "写入光盘", unknownArg(stream.unknown))
	if err != nil {
		return err
	}
	_ = result.Clear()
	return nil
}

// Erase 擦除可擦写介质（full=false 为快速擦除）
func (writer *DataWriter) Erase(full bool, heartbeat func(elapsed time.Duration)) error {
	eraser, err := createObject(CLSID_FORMAT2_ERASE)
	if err != nil {
		return err
	}
	defer eraser.Release()
	if err := putValue(eraser, "Recorder", "刻录机", writer.recorder); err != nil {
		return err
	}
	if err := putValue(eraser, "FullErase", "擦除模式", full); err != nil {
		return err
	}
	if err := putValue(eraser, "ClientName", "客户端名", CLIENT_NAME); err != nil {
		return err
	}
	stop := startHeartbeat(heartbeat)
	defer close(stop)
	return callVoid(eraser, "EraseMedia", "擦除光盘")
}

// Eject 弹出光盘
func (writer *DataWriter) Eject() error {
	return callVoid(writer.recorder, "EjectMedia", "弹出光盘")
}

// mountedVolumeLabel Windows 给这张盘显示的卷标——资源管理器里看到的那个名字，Unicode 无损。
// 追加区段沿用盘名以它为准：引擎的 ImportedVolumeName 要过本机 ANSI 代码页、遇 0x00 截断，中文名读回即失真
// （2026-11-24 内置光驱实测）。盘没有挂载点（没盘符）或 Windows 读不出时返回空串，调用方退回引擎读回值
func (writer *DataWriter) mountedVolumeLabel() string {
	if writer.info.VolumePath == "" {
		return ""
	}
	pathPointer, err := windows.UTF16PtrFromString(writer.info.VolumePath)
	if err != nil {
		return ""
	}
	var nameBuffer [windows.MAX_PATH + 1]uint16
	err = windows.GetVolumeInformation(pathPointer, &nameBuffer[0], uint32(len(nameBuffer)), nil, nil, nil, nil, 0)
	if err != nil {
		return ""
	}
	return cleanVolumeName([]rune(windows.UTF16ToString(nameBuffer[:])))
}

// applyVolumeName 定卷标并返回最终使用的卷标（空串=交给引擎自定）：规则见 chooseVolumeName
// （引擎导入上一区段后并不自动沿用卷标，镜像里的卷标是空的，缺省会换成时间戳卷标
// ——2026-11-24 实测：追加后盘名由 ZPACK_TEST_VOLUME 变成了 2026.11.24_0118）
func applyVolumeName(image *ole.IDispatch, explicit, mounted string, appended bool) (string, error) {
	if volumeName := chooseVolumeName(explicit, mounted, "", appended); volumeName != "" {
		return volumeName, putValue(image, "VolumeName", "卷标", volumeName)
	}
	if explicit != "" || !appended {
		return "", nil
	}
	imported, err := getString(image, "ImportedVolumeName", "盘上卷标")
	if err != nil || imported == "" {
		return "", err
	}
	if volumeName := sanitizeVolumeName(imported); volumeName != "" {
		return volumeName, putValue(image, "VolumeName", "卷标", volumeName)
	}
	return "", nil // 读不出可写回的卷标 → 不动，由引擎自定
}

// NewImage 创建镜像并按介质状态配置：非空白盘导入上一区段（既有文件在新区段中保持可见）；
// 卷标规则见 applyVolumeName
func (writer *DataWriter) NewImage(volumeName string, media Media) (*ImageBuilder, error) {
	image, err := createObject(CLSID_FILE_SYSTEM_IMAGE)
	if err != nil {
		return nil, err
	}
	builder := &ImageBuilder{image: image}
	// 多区段接口必须来自已绑定刻录机的写入器，且须先于其他配置设置
	sessionsValue, err := oleutil.GetProperty(writer.writer, "MultisessionInterfaces")
	if err != nil {
		builder.Close()
		return nil, wrapCall("读取多区段接口", err)
	}
	builder.multisession = sessionsValue // 数组由该属性值持有，镜像对象引用期间保持存活
	result, putErr := invokeRaw(image, "MultisessionInterfaces", ole.DISPATCH_PROPERTYPUT, "设置多区段接口", *sessionsValue)
	if putErr != nil && !(media.HeuristicallyBlank && IsSCode(putErr, SCODE_IMPORT_SEEK_FAILURE)) {
		builder.Close()
		return nil, putErr
	}
	// 空白的一次写入介质上引擎按文档返回 IMPORT_SEEK_FAILURE——无上一区段可导入，正常继续
	if result != nil {
		_ = result.Clear()
	}

	// 文件系统：三种全建，限于当前介质与多区段类型支持的范围
	supported, err := getInt(image, "FileSystemsSupported", "支持的文件系统")
	if err == nil && supported != 0 {
		err = putValue(image, "FileSystemsToCreate", "文件系统", FSI_FILESYSTEMS_ALL&supported)
	}
	appended := !media.HeuristicallyBlank
	if err == nil && appended {
		err = callVoid(image, "ImportFileSystem", "导入上一区段")
	}
	if err == nil {
		builder.volumeName, err = applyVolumeName(image, volumeName, writer.mountedVolumeLabel(), appended)
	}
	var rootValue *ole.VARIANT
	if err == nil {
		rootValue, err = oleutil.GetProperty(image, "Root")
	}
	if builder.root, err = takeDispatch(rootValue, err, "读取镜像根目录"); err != nil {
		builder.Close()
		return nil, err
	}
	return builder, nil
}

// ImageBuilder 光盘镜像构建器：目录 / 散文件 → 镜像（系统镜像库 IFileSystemImage）。
// 文件数据在写入时按需从磁盘读取，不落临时文件
type ImageBuilder struct {
	image        *ole.IDispatch // IFileSystemImage
	root         *ole.IDispatch // IFsiDirectoryItem：镜像根
	result       *ole.IDispatch // IFileSystemImageResult（Build 后持有到 Close）
	multisession *ole.VARIANT   // 多区段接口数组
	volumeName   string         // 最终使用的卷标（空=引擎自定）
}

// AddTreeContents 把磁盘目录的全部内容加入镜像根（引擎自己遍历目录）
func (builder *ImageBuilder) AddTreeContents(directory string) error {
	return callVoid(builder.root, "AddTree", "加入目录", directory, false)
}

// AddEntry 以 entryName 为名把一个文件或目录加入镜像根（目录则整树装入同名目录）
func (builder *ImageBuilder) AddEntry(entryName, path string, isDirectory bool) error {
	if !isDirectory {
		return builder.addFile(entryName, path)
	}
	if err := callVoid(builder.root, "AddDirectory", "建目录", entryName); err != nil {
		return err
	}
	entryValue, err := oleutil.GetProperty(builder.root, "Item", entryName) // 带参属性
	entry, entryErr := takeDispatch(entryValue, err, "读取镜像目录")
	if entryErr != nil {
		return entryErr
	}
	defer entry.Release()
	return callVoid(entry, "AddTree", "加入目录", path, false)
}

// addFile 以 entryName 为名把一个文件加入镜像根（文件流交给引擎持有，写入时按需读取）
func (builder *ImageBuilder) addFile(entryName, path string) error {
	stream, err := OpenFileStream(path)
	if err != nil {
		return err
	}
	defer stream.Release()
	nameArg, freeName := bstrArg(entryName)
	defer freeName()
	result, err := invokeRaw(builder.root, "AddFile", ole.DISPATCH_METHOD, "加入文件", nameArg, unknownArg(stream.unknown))
	if err != nil {
		return err
	}
	_ = result.Clear()
	return nil
}

// FileCount 镜像内的文件数（含导入的上一区段）
func (builder *ImageBuilder) FileCount() (int32, error) {
	return getInt(builder.image, "FileCount", "镜像文件数")
}

// VolumeName 最终使用的卷标（空=交给引擎自定）
func (builder *ImageBuilder) VolumeName() string {
	return builder.volumeName
}

// Build 生成镜像，返回交给写入器的数据流（调用方用完 Release）与镜像字节数
func (builder *ImageBuilder) Build() (*Stream, int64, error) {
	resultValue, callErr := oleutil.CallMethod(builder.image, "CreateResultImage")
	result, err := takeDispatch(resultValue, callErr, "生成镜像")
	if err != nil {
		return nil, 0, err
	}
	builder.result = result
	totalBlocks, err := getInt(builder.result, "TotalBlocks", "镜像扇区数")
	if err != nil {
		return nil, 0, err
	}
	blockSize, err := getInt(builder.result, "BlockSize", "扇区大小")
	if err != nil {
		return nil, 0, err
	}
	streamValue, err := oleutil.GetProperty(builder.result, "ImageStream")
	unknown, err := takeUnknown(streamValue, err, "读取镜像流")
	if err != nil {
		return nil, 0, err
	}
	return &Stream{unknown: unknown}, int64(totalBlocks) * int64(blockSize), nil
}

// Close 释放镜像构建器（须在镜像流写完之后调用）
func (builder *ImageBuilder) Close() {
	if builder == nil {
		return
	}
	if builder.root != nil {
		builder.root.Release()
		builder.root = nil
	}
	if builder.result != nil {
		builder.result.Release()
		builder.result = nil
	}
	if builder.multisession != nil {
		_ = builder.multisession.Clear()
		builder.multisession = nil
	}
	if builder.image != nil {
		builder.image.Release()
		builder.image = nil
	}
}

// Stream 交给写入器的数据流（COM IStream）：镜像文件用系统自带的文件流，目录内容由镜像库的结果流提供
type Stream struct {
	unknown *ole.IUnknown
}

// Release 释放流
func (stream *Stream) Release() {
	if stream != nil && stream.unknown != nil {
		stream.unknown.Release()
		stream.unknown = nil
	}
}

// OpenFileStream 把文件打开为只读流
func OpenFileStream(path string) (*Stream, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var unknown *ole.IUnknown
	ret, _, _ := procSHCreateStreamOnFileEx.Call(
		uintptr(unsafe.Pointer(pathPtr)), stgmRead|stgmShareDenyNone, 0, 0, 0, uintptr(unsafe.Pointer(&unknown)))
	if int32(ret) < 0 {
		return nil, fmt.Errorf("打开文件为流失败: %s（0x%08X）", path, uint32(ret))
	}
	return &Stream{unknown: unknown}, nil
}

// startHeartbeat 每 HEARTBEAT_INTERVAL 回调一次已用时长；close 返回的通道即停止
func startHeartbeat(heartbeat func(elapsed time.Duration)) chan struct{} {
	stop := make(chan struct{})
	if heartbeat == nil {
		return stop
	}
	started := time.Now()
	go func() {
		ticker := time.NewTicker(HEARTBEAT_INTERVAL)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				heartbeat(time.Since(started))
			}
		}
	}()
	return stop
}
