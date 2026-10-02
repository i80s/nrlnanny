package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var recorder *Recorder

const (
	sampleRate        = 8000
	bitsPerSample     = 16
	channels          = 1 // Mono
	minRecordDuration = 2 * time.Second
)

// WAVHeader 定义WAV文件头结构
type WAVHeader struct {
	RIFFID        [4]byte // "RIFF"
	FileSize      uint32
	WAVEID        [4]byte // "WAVE"
	FMTID         [4]byte // "fmt "
	FMTSize       uint32  // 16 for PCM
	AudioFormat   uint16  // 1 for PCM
	NumChannels   uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
	DATAID        [4]byte // "data"
	DataSize      uint32
}

// createWAVHeader 创建WAV文件头
func createWAVHeader(dataSize uint32) WAVHeader {
	byteRate := uint32(sampleRate * channels * bitsPerSample / 16)
	blockAlign := uint16(channels * bitsPerSample / 16)

	return WAVHeader{
		RIFFID:        [4]byte{'R', 'I', 'F', 'F'},
		FileSize:      36 + dataSize, // 36 bytes for header + dataSize
		WAVEID:        [4]byte{'W', 'A', 'V', 'E'},
		FMTID:         [4]byte{'f', 'm', 't', ' '},
		FMTSize:       16,
		AudioFormat:   1, // PCM
		NumChannels:   channels,
		SampleRate:    sampleRate,
		ByteRate:      byteRate,
		BlockAlign:    blockAlign,
		BitsPerSample: bitsPerSample,
		DATAID:        [4]byte{'d', 'a', 't', 'a'},
		DataSize:      dataSize,
	}
}

// Recorder 结构体用于管理录音过程
type Recorder struct {
	speakerCallsign string
	outputDir       string
	mu              sync.Mutex
	currentBuffer   *bytes.Buffer
	recordStartTime time.Time
	isRecording     bool
	lastDataTime    time.Time
}

// NewRecorder 创建一个新的Recorder实例
func NewRecorder(speakerCallsign string) *Recorder {
	return &Recorder{
		speakerCallsign: speakerCallsign,
		outputDir:       conf.System.RecoderFilePath,
		currentBuffer:   new(bytes.Buffer),
	}
}

// ProcessPCMData 处理传入的PCM数据
func (r *Recorder) ProcessPCMData(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 如果当前没有录音，并且数据长度大于0，则开始记录
	if !r.isRecording && len(data) > 0 {
		r.recordStartTime = time.Now()
		r.isRecording = true
		r.currentBuffer.Reset() // 清空缓冲区，准备开始新录音
		log.Printf("[%s] 开始讲话...\n", r.speakerCallsign)
	}

	if r.isRecording {
		// 写入数据
		r.currentBuffer.Write(data)
		r.lastDataTime = time.Now()

		// 检查是否需要结束当前录音并保存
		r.checkAndSaveRecord()
	}
}

// checkAndSaveRecord 检查是否需要保存当前录音
func (r *Recorder) checkAndSaveRecord() {
	if !r.isRecording {
		return
	}

	// 计算当前录音时长
	currentDuration := time.Since(r.recordStartTime)

	// 如果数据停止超过2秒，或者当前录音时长超过某个阈值（例如，我们可以设定最大文件时长）
	// 这里我们主要关注数据停止超过2秒的情况
	if time.Since(r.lastDataTime) > minRecordDuration {
		r.saveCurrentRecord(currentDuration)
		r.isRecording = false
		log.Printf("[%s] 结束记录。\n", r.speakerCallsign)
	}
}

// saveCurrentRecord 保存当前录音到文件
func (r *Recorder) saveCurrentRecord(duration time.Duration) {

	// 确保录音时长大于最小记录时长
	if r.currentBuffer.Len() < 32000 || duration < minRecordDuration {
		log.Printf("[%s] 录音时长不足 %.2f 秒，或者数据太少不保存。\n", r.speakerCallsign, minRecordDuration.Seconds())
		r.currentBuffer.Reset()
		return
	}

	// 获取日期作为子目录
	today := time.Now().Format("2006-01-02")
	dayOutputDir := filepath.Join(r.outputDir, today)
	if err := os.MkdirAll(dayOutputDir, 0755); err != nil {
		log.Printf("创建目录 %s 失败: %v\n", dayOutputDir, err)
		return
	}

	// 构造文件名
	startTimeStr := r.recordStartTime.Format("2006-01-02_150405") // HHMMSS
	durationSeconds := int(duration.Seconds())
	filename := fmt.Sprintf("%s_%s_%ds.wav", startTimeStr, r.speakerCallsign, durationSeconds)
	filePath := filepath.Join(dayOutputDir, filename)

	log.Printf("[%s] 保存录音到: %s (时长:%d秒)\n", r.speakerCallsign, filePath, durationSeconds)

	// 创建WAV文件
	file, err := os.Create(filePath)
	if err != nil {
		log.Printf("创建文件 %s 失败: %v, %v\n", filePath, err, []byte(filePath))
		return
	}
	defer file.Close()

	// 计算PCM数据大小
	pcmDataSize := uint32(r.currentBuffer.Len())

	// 创建并写入WAV头
	wavHeader := createWAVHeader(pcmDataSize)
	if err := binary.Write(file, binary.LittleEndian, wavHeader); err != nil {
		log.Printf("写入WAV头失败: %v\n", err)
		return
	}

	// 写入PCM数据
	if _, err := io.Copy(file, r.currentBuffer); err != nil {
		log.Printf("写入PCM数据失败: %v\n", err)
		return
	}

	// 清空缓冲区以便下一次录音
	r.currentBuffer.Reset()
}

// Stop 停止录音，并保存所有未保存的数据
func (r *Recorder) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isRecording {
		r.saveCurrentRecord(time.Since(r.recordStartTime))
		r.isRecording = false
		//log.Printf("[%s] 录音停止。\n", r.speakerCallsign)
	}

}

func StartRecoder() {

	if conf.System.RecoderFilePath == "" {
		log.Printf("录音文件路径未配置，录音失败")
		return
	}
	// 示例用法
	baseOutputDir := conf.System.RecoderFilePath
	if err := os.MkdirAll(baseOutputDir, 0755); err != nil {
		log.Printf("创建基础输出目录 %s 失败: %v\n", baseOutputDir, err)
		return
	}

	// 添加调试日志
	absPath, _ := filepath.Abs(baseOutputDir)
	log.Printf("基础输出目录绝对路径: %s\n", absPath)

	// 创建一个文件用于测试写入权限
	testFile := filepath.Join(baseOutputDir, "test.txt")
	log.Printf("创建测试文件: %s\n", testFile)
	f, err := os.Create(testFile)
	if err != nil {
		log.Printf("创建测试文件失败: %v\n", err)
	} else {
		f.WriteString("测试文件写入成功\n")
		f.Close()
	}

	// 修改录音器实例
	// 添加更详细的日志输出已被移除，使用原有方式
	// 修改录音器实例
	// 添加更详细的日志输出已被移除，使用原有方式

}
