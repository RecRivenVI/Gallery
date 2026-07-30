package security_test

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/platform/process"
	"github.com/RecRivenVI/gallery/internal/platform/tooldiscovery"
	"github.com/RecRivenVI/gallery/internal/storage"
	"github.com/RecRivenVI/gallery/internal/toolrunner"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
)

const (
	maliciousMediaGateEnv = "GALLERY_TEST_MALICIOUS_MEDIA"
	ffprobePathEnv        = "GALLERY_TEST_FFPROBE_PATH"
	ffprobeVersionEnv     = "GALLERY_TEST_FFPROBE_VERSION"
	ffprobeSHA256Env      = "GALLERY_TEST_FFPROBE_SHA256"
	ffmpegPathEnv         = "GALLERY_TEST_FFMPEG_PATH"
	ffmpegVersionEnv      = "GALLERY_TEST_FFMPEG_VERSION"
	ffmpegSHA256Env       = "GALLERY_TEST_FFMPEG_SHA256"
	mediaCorpusReportEnv  = "GALLERY_TEST_MEDIA_CORPUS_REPORT"

	mediaToolTimeoutSeconds = int64(5)
	mediaToolCPUSeconds     = int64(2)
	mediaToolMemoryBytes    = int64(256 << 20)
	mediaToolOutputBytes    = int64(64 << 10)
	maxCorpusFileBytes      = int64(2 << 20)
	maxCorpusTotalBytes     = int64(4 << 20)
)

type mediaToolPin struct {
	id, path, version, sha256 string
}

type mediaCorpusCase struct {
	name               string
	filename           string
	body               []byte
	decode             bool
	wantProbeComplete  bool
	wantProbeFail      bool
	wantDecodeComplete bool
	wantDecodeFail     bool
}

type mediaExecution struct {
	outcome string
	elapsed time.Duration
}

type mediaFileFact struct {
	size    int64
	modTime int64
	digest  [sha256.Size]byte
}

// TestMaliciousMediaCorpusDefinition 让普通 CI 也能验证语料本身没有被悄悄删减或膨胀。
// 它不启动外部工具，但会实际构造压缩内容并检查声明展开量与落盘硬边界。
func TestMaliciousMediaCorpusDefinition(t *testing.T) {
	cases := buildMediaCorpus(t, "127.0.0.1:9")
	if len(cases) != 13 {
		t.Fatalf("恶意媒体语料数 = %d，期望 13", len(cases))
	}
	root := t.TempDir()
	if err := writeMediaCorpus(root, cases); err != nil {
		t.Fatal(err)
	}
	facts, total, err := snapshotMediaCorpus(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != len(cases) || total > maxCorpusTotalBytes {
		t.Fatalf("语料边界失守: files=%d/%d bytes=%d/%d", len(facts), len(cases), total, maxCorpusTotalBytes)
	}
	required := map[string]bool{
		"valid-png-control": false, "png-deflate-dimension-bomb": false,
		"mp4-deep-box-nesting": false, "zip-high-ratio-attachment": false,
		"hls-external-reference": false,
	}
	var zipBody []byte
	for _, sample := range cases {
		if _, exists := required[sample.name]; exists {
			required[sample.name] = true
		}
		if sample.name == "zip-high-ratio-attachment" {
			zipBody = sample.body
		}
	}
	for name, present := range required {
		if !present {
			t.Fatalf("恶意媒体语料缺少类别 %s", name)
		}
	}
	const pngExpandedBytes = int64(16_384) * (16_384 + 1)
	if pngExpandedBytes <= mediaToolMemoryBytes {
		t.Fatalf("PNG 解压尺寸 %d 未超过工具内存门禁 %d", pngExpandedBytes, mediaToolMemoryBytes)
	}
	archive, err := zip.NewReader(bytes.NewReader(zipBody), int64(len(zipBody)))
	if err != nil {
		t.Fatalf("ZIP 高压缩比样本不可解析: %v", err)
	}
	if len(archive.File) != 1 || archive.File[0].UncompressedSize64 != 64<<20 || len(zipBody) >= 1<<20 {
		t.Fatalf("ZIP 高压缩比前提不成立: files=%d compressed=%d", len(archive.File), len(zipBody))
	}
}

// TestMaliciousMediaCorpusWithPinnedTools 是阶段 5 的默认关闭真实工具门禁。语料全部在
// 临时 AppDirs 内按字节生成，不读取真实 Source 或媒体；只有显式启用并同时提供两份
// 工具的绝对路径、精确 version token、SHA-256 与报告路径时才会执行。
//
// 这条门禁验证的安全结论是“主进程不解析不可信容器，受 pin 的工具在协议/格式白名单、
// 持久预算和 Windows Job Object 下有界收敛”。它不是第三方解码器漏洞扫描，也不声称
// 这些小型合成样本穷举了真实世界恶意文件。
func TestMaliciousMediaCorpusWithPinnedTools(t *testing.T) {
	if strings.TrimSpace(os.Getenv(maliciousMediaGateEnv)) != "1" {
		t.Skipf("未设置 %s=1，跳过恶意媒体真实工具门禁", maliciousMediaGateEnv)
	}
	if runtime.GOOS != "windows" {
		t.Skip("当前只有 Windows Job Object 实现了外部工具进程树硬限制")
	}
	if testing.Short() {
		t.Skip("短模式跳过恶意媒体真实工具门禁")
	}
	probe := requireMediaToolPin(t, "ffprobe", ffprobePathEnv, ffprobeVersionEnv, ffprobeSHA256Env)
	decode := requireMediaToolPin(t, "ffmpeg", ffmpegPathEnv, ffmpegVersionEnv, ffmpegSHA256Env)
	reportPath := requireAbsoluteEnvPath(t, mediaCorpusReportEnv, false)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{}, 1)
	acceptErrors := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			acceptErrors <- acceptErr
			return
		}
		_ = connection.Close()
		accepted <- struct{}{}
	}()

	root := filepath.Join(t.TempDir(), "app")
	dirs := appdirs.UnderRoot(root)
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	corpusRoot := filepath.Join(dirs.Temp, "malicious-media-corpus")
	if err := os.MkdirAll(corpusRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := buildMediaCorpus(t, listener.Addr().String())
	if err := writeMediaCorpus(corpusRoot, cases); err != nil {
		t.Fatal(err)
	}
	before, totalBytes, err := snapshotMediaCorpus(corpusRoot)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)}
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, identity.NewGenerator(now))
	if err != nil {
		t.Fatal(err)
	}
	controller := process.Controller{WaitDelay: 5 * time.Second}
	discovery, err := tooldiscovery.New(ctx, []tooldiscovery.Declaration{
		{ID: probe.id, Path: probe.path, Version: probe.version, SHA256: probe.sha256},
		{ID: decode.id, Path: decode.path, Version: decode.version, SHA256: decode.sha256},
	}, controller)
	if err != nil {
		t.Fatalf("受 pin 外部工具启动验证失败: %v", err)
	}
	service, err := toolrunner.New(jobStore, controller, discovery)
	if err != nil {
		t.Fatal(err)
	}

	record := report.Report{
		SchemaVersion: 2,
		Scenario:      "stage5-malicious-media-corpus",
		ScenarioAlias: "synthetic-adversarial-media-v1",
		Tier:          "integration-real-tool",
		Transport:     "in-process-job-service",
		Scale:         len(cases),
		Limitations: []string{
			"语料为纯合成的小型结构攻击样本，不是 coverage-guided fuzz 或第三方 CVE 全集",
			"只验证当前显式 pin 的 Windows 工具制品与 Job Object；不外推到非 Windows",
		},
	}
	record.Add("tool-pin/ffprobe", discovery.Available("ffprobe"), safeToolPinDetail(probe))
	record.Add("tool-pin/ffmpeg", discovery.Available("ffmpeg"), safeToolPinDetail(decode))
	record.Add("corpus/bounds", len(before) == len(cases) && totalBytes <= maxCorpusTotalBytes,
		fmt.Sprintf("files=%d bytes=%d maxFileBytes=%d maxTotalBytes=%d", len(before), totalBytes, maxCorpusFileBytes, maxCorpusTotalBytes))
	record.Add("execution/budgets", true,
		fmt.Sprintf("serial=true wallSeconds=%d cpuSeconds=%d memoryBytes=%d outputBytesPerStream=%d protocol=file formatAllowlist=true",
			mediaToolTimeoutSeconds, mediaToolCPUSeconds, mediaToolMemoryBytes, mediaToolOutputBytes))

	for _, sample := range cases {
		probeRun := executeMediaTool(t, ctx, service, jobStore, toolrunner.Request{
			ToolID: "ffprobe", Args: ffprobeArgs(sample.filename), WorkingDir: corpusRoot,
			TimeoutSeconds: mediaToolTimeoutSeconds, MaxOutputBytes: mediaToolOutputBytes,
			MaxMemoryBytes: mediaToolMemoryBytes, MaxCPUTimeSeconds: mediaToolCPUSeconds,
		})
		probePass := probeRun.outcome == "completed" || probeRun.outcome == "failed"
		if sample.wantProbeComplete {
			probePass = probePass && probeRun.outcome == "completed"
		}
		if sample.wantProbeFail {
			probePass = probePass && probeRun.outcome == "failed"
		}
		record.Add("probe/"+sample.name, probePass && probeRun.elapsed <= 12*time.Second,
			mediaExecutionDetail(probeRun))

		if !sample.decode {
			continue
		}
		decodeRun := executeMediaTool(t, ctx, service, jobStore, toolrunner.Request{
			ToolID: "ffmpeg", Args: ffmpegArgs(sample.filename), WorkingDir: corpusRoot,
			TimeoutSeconds: mediaToolTimeoutSeconds, MaxOutputBytes: mediaToolOutputBytes,
			MaxMemoryBytes: mediaToolMemoryBytes, MaxCPUTimeSeconds: mediaToolCPUSeconds,
		})
		decodePass := decodeRun.outcome == "completed" || decodeRun.outcome == "failed"
		if sample.wantDecodeComplete {
			decodePass = decodePass && decodeRun.outcome == "completed"
		}
		if sample.wantDecodeFail {
			decodePass = decodePass && decodeRun.outcome == "failed"
		}
		record.Add("decode/"+sample.name, decodePass && decodeRun.elapsed <= 12*time.Second,
			mediaExecutionDetail(decodeRun))
	}

	// HLS 样本含一个动态 loopback URL；协议与 demuxer 白名单必须在建立连接前拒绝它。
	time.Sleep(300 * time.Millisecond)
	networkDenied := true
	select {
	case <-accepted:
		networkDenied = false
	case acceptErr := <-acceptErrors:
		if !errors.Is(acceptErr, net.ErrClosed) {
			networkDenied = false
		}
	default:
	}
	record.Add("external-reference/network-denied", networkDenied, "")

	after, _, snapshotErr := snapshotMediaCorpus(corpusRoot)
	record.Add("corpus/read-only", snapshotErr == nil && equalMediaSnapshots(before, after), "")
	if err := record.Save(reportPath); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(corpusRoot)) || bytes.Contains(encoded, []byte(probe.path)) || bytes.Contains(encoded, []byte(decode.path)) {
		t.Fatal("恶意媒体报告泄露工具或临时语料绝对路径")
	}
	if record.FailureCount != 0 {
		t.Fatalf("恶意媒体语料门禁失败: failures=%d findings=%d", record.FailureCount, len(record.Findings))
	}
	t.Logf("恶意媒体语料门禁通过: cases=%d findings=%d bytes=%d failures=0", len(cases), len(record.Findings), totalBytes)
}

func requireMediaToolPin(t *testing.T, id, pathEnv, versionEnv, digestEnv string) mediaToolPin {
	t.Helper()
	path := requireAbsoluteEnvPath(t, pathEnv, true)
	base := filepath.Base(path)
	if !strings.EqualFold(base, id) && !strings.EqualFold(base, id+".exe") {
		t.Fatalf("%s 只接受名为 %s/%s.exe 的工具", pathEnv, id, id)
	}
	version := strings.TrimSpace(os.Getenv(versionEnv))
	digest := strings.ToLower(strings.TrimSpace(os.Getenv(digestEnv)))
	if version == "" || strings.ContainsAny(version, " \t\r\n") {
		t.Fatalf("%s 必须是非空单 token", versionEnv)
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		t.Fatalf("%s 必须是 64 位 SHA-256", digestEnv)
	}
	return mediaToolPin{id: id, path: path, version: version, sha256: digest}
}

func requireAbsoluteEnvPath(t *testing.T, name string, mustExist bool) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" || !filepath.IsAbs(value) {
		t.Fatalf("%s 必须是显式绝对路径", name)
	}
	if mustExist {
		info, err := os.Stat(value)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s 未指向可读普通文件", name)
		}
	} else if err := os.MkdirAll(filepath.Dir(value), 0o700); err != nil {
		t.Fatalf("创建 %s 父目录失败: %v", name, err)
	}
	return filepath.Clean(value)
}

func safeToolPinDetail(pin mediaToolPin) string {
	return fmt.Sprintf("version=%s sha256=%s", pin.version, pin.sha256)
}

func executeMediaTool(t *testing.T, ctx context.Context, service *toolrunner.Service, store *jobs.Store, request toolrunner.Request) mediaExecution {
	t.Helper()
	job, err := service.Create(ctx, request, "security-testlab")
	if err != nil {
		t.Fatalf("创建 %s 语料 Job: %v", request.ToolID, err)
	}
	started := time.Now()
	executeErr := service.Execute(ctx, job.ID)
	elapsed := time.Since(started)
	terminal, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch terminal.Status {
	case jobs.StatusCompleted:
		if executeErr != nil {
			t.Fatalf("%s Job 已 completed 但 Execute 返回错误: %v", request.ToolID, executeErr)
		}
		var result toolrunner.Result
		if err := json.Unmarshal(terminal.ResultJSON, &result); err != nil {
			t.Fatalf("%s completed 结果不可解析: %v", request.ToolID, err)
		}
		if result.StdoutBytes > request.MaxOutputBytes || result.StderrBytes > request.MaxOutputBytes {
			t.Fatalf("%s completed 结果越过输出上限: stdout=%d stderr=%d", request.ToolID, result.StdoutBytes, result.StderrBytes)
		}
		return mediaExecution{outcome: "completed", elapsed: elapsed}
	case jobs.StatusFailed:
		if executeErr == nil || terminal.IssueCode != "EXTERNAL_TOOL_FAILED" || len(terminal.ResultJSON) != 0 {
			t.Fatalf("%s failed Job 不满足稳定失败契约: err=%v code=%s resultBytes=%d",
				request.ToolID, executeErr, terminal.IssueCode, len(terminal.ResultJSON))
		}
		return mediaExecution{outcome: "failed", elapsed: elapsed}
	default:
		t.Fatalf("%s Job 未收敛到终态: %s", request.ToolID, terminal.Status)
		return mediaExecution{}
	}
}

func mediaExecutionDetail(run mediaExecution) string {
	return fmt.Sprintf("outcome=%s elapsedMs=%d", run.outcome, run.elapsed.Milliseconds())
}

func ffprobeArgs(filename string) []string {
	return append(ffprobeInputGuards(),
		"-show_entries", "format=format_name,duration,size:stream=index,codec_type,codec_name,width,height",
		"-of", "json", filename)
}

func ffmpegArgs(filename string) []string {
	return append(ffmpegInputGuards(), "-i", filename, "-map", "0:v:0?", "-frames:v", "1",
		"-map", "0:a:0?", "-frames:a", "1", "-sn", "-dn", "-f", "null", "-")
}

func ffprobeInputGuards() []string {
	return []string{
		"-v", "error",
		"-protocol_whitelist", "file",
		"-format_whitelist", "avi,flac,gif,image2,jpeg_pipe,mjpeg,matroska,webm,mov,mp3,ogg,png_pipe,wav",
		"-max_alloc", "268435456",
		"-probesize", "1048576",
		"-analyzeduration", "1000000",
		"-max_probe_packets", "256",
		"-threads", "1",
	}
}

func ffmpegInputGuards() []string {
	return append([]string{"-nostdin"}, ffprobeInputGuards()...)
}

func buildMediaCorpus(t *testing.T, loopbackAddress string) []mediaCorpusCase {
	t.Helper()
	validPNG := encodeValidPNG(t)
	return []mediaCorpusCase{
		{name: "valid-png-control", filename: "control.png", body: validPNG, decode: true, wantProbeComplete: true, wantDecodeComplete: true},
		{name: "png-deflate-dimension-bomb", filename: "dimension-bomb.png", body: encodePNGDimensionBomb(t), decode: true, wantDecodeFail: true},
		{name: "jpeg-max-dimensions-truncated", filename: "max-dimensions.jpg", body: jpegDimensionBomb(), decode: true, wantDecodeFail: true},
		{name: "gif-max-canvas-truncated", filename: "max-canvas.gif", body: gifDimensionBomb(), decode: true, wantDecodeFail: true},
		{name: "mp4-extended-size-truncated", filename: "extended-size.mp4", body: truncatedMP4(), decode: true, wantDecodeFail: true},
		{name: "mp4-deep-box-nesting", filename: "deep-boxes.mp4", body: nestedMP4(384), decode: true, wantDecodeFail: true},
		{name: "matroska-unknown-size-nesting", filename: "unknown-size.mkv", body: nestedEBML(512), decode: false},
		{name: "riff-oversized-chunk", filename: "oversized.avi", body: oversizedRIFF(), decode: false},
		{name: "ogg-missing-laced-payload", filename: "missing-payload.ogg", body: truncatedOgg(), decode: false},
		{name: "flac-oversized-metadata", filename: "oversized.flac", body: truncatedFLAC(), decode: false},
		{name: "id3-max-synchsafe-size", filename: "oversized.mp3", body: truncatedID3(), decode: false},
		{name: "zip-high-ratio-attachment", filename: "compression-bomb.zip", body: encodeZIPBomb(t), decode: false, wantProbeFail: true},
		{name: "hls-external-reference", filename: "external.m3u8", body: []byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nhttp://" + loopbackAddress + "/segment.ts\n#EXT-X-ENDLIST\n"), decode: false, wantProbeFail: true},
	}
}

func writeMediaCorpus(root string, cases []mediaCorpusCase) error {
	seen := make(map[string]struct{}, len(cases))
	var total int64
	for _, sample := range cases {
		if sample.name == "" || sample.filename == "" || filepath.Base(sample.filename) != sample.filename {
			return fmt.Errorf("语料名称或文件名无效")
		}
		if _, exists := seen[sample.name]; exists {
			return fmt.Errorf("语料名称重复: %s", sample.name)
		}
		seen[sample.name] = struct{}{}
		if int64(len(sample.body)) > maxCorpusFileBytes {
			return fmt.Errorf("语料 %s 超过单文件上限", sample.name)
		}
		total += int64(len(sample.body))
		if total > maxCorpusTotalBytes {
			return fmt.Errorf("语料总大小超过上限")
		}
		if err := os.WriteFile(filepath.Join(root, sample.filename), sample.body, 0o400); err != nil {
			return err
		}
	}
	return nil
}

func snapshotMediaCorpus(root string) (map[string]mediaFileFact, int64, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, err
	}
	result := make(map[string]mediaFileFact, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || filesystem.IsLink(entry.Type()) {
			return nil, 0, fmt.Errorf("语料目录含非普通文件")
		}
		path := filepath.Join(root, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, 0, err
		}
		info, err := entry.Info()
		if err != nil {
			return nil, 0, err
		}
		result[entry.Name()] = mediaFileFact{size: info.Size(), modTime: info.ModTime().UnixNano(), digest: sha256.Sum256(body)}
		total += info.Size()
	}
	return result, total, nil
}

func equalMediaSnapshots(left, right map[string]mediaFileFact) bool {
	if len(left) != len(right) {
		return false
	}
	for name, fact := range left {
		if right[name] != fact {
			return false
		}
	}
	return true
}

func encodeValidPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	value.SetNRGBA(0, 0, color.NRGBA{R: 0x4a, G: 0x78, B: 0xa8, A: 0xff})
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func encodePNGDimensionBomb(t *testing.T) []byte {
	t.Helper()
	const width, height = uint32(16_384), uint32(16_384)
	var output bytes.Buffer
	output.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8], ihdr[9] = 8, 0 // 8-bit grayscale
	writePNGChunk(&output, "IHDR", ihdr)
	var compressed bytes.Buffer
	encoder := zlib.NewWriter(&compressed)
	if _, err := io.CopyN(encoder, zeroReader{}, int64(height)*(int64(width)+1)); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	writePNGChunk(&output, "IDAT", compressed.Bytes())
	writePNGChunk(&output, "IEND", nil)
	return output.Bytes()
}

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = 0
	}
	return len(value), nil
}

func writePNGChunk(output *bytes.Buffer, kind string, body []byte) {
	_ = binary.Write(output, binary.BigEndian, uint32(len(body)))
	output.WriteString(kind)
	output.Write(body)
	crc := crc32.NewIEEE()
	_, _ = io.WriteString(crc, kind)
	_, _ = crc.Write(body)
	_ = binary.Write(output, binary.BigEndian, crc.Sum32())
}

func jpegDimensionBomb() []byte {
	return []byte{
		0xff, 0xd8,
		0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0,
		0xff, 0xc0, 0, 17, 8, 0xff, 0xff, 0xff, 0xff, 3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0,
		0xff, 0xda, 0, 12, 3, 1, 0, 2, 0, 3, 0, 0, 63, 0,
		0xff, 0xd9,
	}
}

func gifDimensionBomb() []byte {
	return []byte{
		'G', 'I', 'F', '8', '9', 'a', 0xff, 0xff, 0xff, 0xff, 0x80, 0, 0,
		0, 0, 0, 0xff, 0xff, 0xff,
		0x2c, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff, 0,
		2, 1, 0, 0, 0x3b,
	}
}

func truncatedMP4() []byte {
	return []byte{
		0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0,
		'i', 's', 'o', 'm', 'i', 's', 'o', '2',
		0, 0, 0, 1, 'm', 'd', 'a', 't', 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	}
}

func nestedMP4(depth int) []byte {
	payload := []byte{0, 0, 0, 8, 'f', 'r', 'e', 'e'}
	for range depth {
		box := make([]byte, 8+len(payload))
		binary.BigEndian.PutUint32(box[0:4], uint32(len(box)))
		copy(box[4:8], "moov")
		copy(box[8:], payload)
		payload = box
	}
	return append(truncatedMP4()[:24:24], payload...)
}

func nestedEBML(depth int) []byte {
	result := []byte{0x1a, 0x45, 0xdf, 0xa3, 0x81, 0x00}
	for range depth {
		result = append(result, 0x18, 0x53, 0x80, 0x67, 0xff)
	}
	return result
}

func oversizedRIFF() []byte {
	result := []byte{'R', 'I', 'F', 'F', 0xff, 0xff, 0xff, 0xff, 'A', 'V', 'I', ' '}
	return append(result, []byte{'L', 'I', 'S', 'T', 0xf0, 0xff, 0xff, 0xff, 'h', 'd', 'r', 'l'}...)
}

func truncatedOgg() []byte {
	result := make([]byte, 27+255)
	copy(result, "OggS")
	result[4] = 0
	result[26] = 255
	for index := 27; index < len(result); index++ {
		result[index] = 255
	}
	return result
}

func truncatedFLAC() []byte {
	return []byte{'f', 'L', 'a', 'C', 0x7f, 0xff, 0xff, 0xff}
}

func truncatedID3() []byte {
	return []byte{'I', 'D', '3', 4, 0, 0, 0x7f, 0x7f, 0x7f, 0x7f}
}

func encodeZIPBomb(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	entry, err := archive.CreateHeader(&zip.FileHeader{Name: "expanded.bin", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(entry, zeroReader{}, 64<<20); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
