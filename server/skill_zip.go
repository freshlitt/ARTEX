package server

import (
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// Go 님 archive/zip 내장형만 해당 Store(0) 그리고 Deflate(8) 압축해제기 2개，다른 방법을 만나면 반환됩니다.
// "zip: unsupported compression algorithm"。압축 소프트웨어는 종종 기본 설정이 아닌 다른 방법을 작성합니다.
// (7-Zip 님 bzip2、WinZip 님 zstd)，그래서 여기에 순수를 넣어 Go 해결할 수 있는 두 가지 보완책；정말 모르겠어요
// (Deflate64 / LZMA / XZ / PPMd / 암호화된 패키지) 압축을 풀기 전에 중국어 안내를 해주세요，하단 레이어를 넣는 대신
// 오류는 그대로 사용자에게 덤프됩니다.。
const (
	zipMethodStore     = 0
	zipMethodDeflate   = 8
	zipMethodDeflate64 = 9
	zipMethodBzip2     = 12
	zipMethodLZMA      = 14
	zipMethodZstdPKW   = 20 // PKWARE 빨리 줘 zstd 할당번호
	zipMethodZstd      = 93
	zipMethodXZ        = 95
	zipMethodJPEG      = 96
	zipMethodWavPack   = 97
	zipMethodPPMd      = 98
	zipMethodAES       = 99
)

var zipMethodNames = map[uint16]string{
	zipMethodStore:     "Store",
	zipMethodDeflate:   "Deflate",
	zipMethodDeflate64: "Deflate64",
	zipMethodBzip2:     "bzip2",
	zipMethodLZMA:      "LZMA",
	zipMethodZstdPKW:   "Zstandard",
	zipMethodZstd:      "Zstandard",
	zipMethodXZ:        "XZ",
	zipMethodJPEG:      "JPEG",
	zipMethodWavPack:   "WavPack",
	zipMethodPPMd:      "PPMd",
	zipMethodAES:       "AES 암호화",
}

func zipMethodName(m uint16) string {
	if n, ok := zipMethodNames[m]; ok {
		return n
	}
	return "알 수 없음"
}

// newSkillZipReader parses an uploaded archive and registers the extra decompressors
// we can support beyond the stdlib's Store/Deflate.
func newSkillZipReader(buf []byte) (*zip.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("압축된 패키지를 구문 분석할 수 없습니다.(해야지 zip 형식)：%w", err)
	}
	zr.RegisterDecompressor(zipMethodBzip2, func(r io.Reader) io.ReadCloser {
		return io.NopCloser(bzip2.NewReader(r))
	})
	zdec := zstd.ZipDecompressor(zstd.WithDecoderConcurrency(1))
	zr.RegisterDecompressor(zipMethodZstd, zdec)
	zr.RegisterDecompressor(zipMethodZstdPKW, zdec)
	return zr, nil
}

// skillZipEntry pairs a zip entry with its decoded (UTF-8) name — f.Name may hold
// raw GBK bytes, see zipEntryName.
type skillZipEntry struct {
	f    *zip.File
	name string
}

// skillZipEntries lists the archive's real files (no directory entries, no archiver
// junk) with their names decoded to UTF-8.
func skillZipEntries(zr *zip.Reader) []skillZipEntry {
	out := make([]skillZipEntry, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := zipEntryName(f)
		if strings.HasPrefix(name, "__MACOSX/") || strings.Contains(name, "/__MACOSX/") ||
			path.Base(name) == ".DS_Store" {
			continue // macOS 포장잔재물
		}
		out = append(out, skillZipEntry{f: f, name: name})
	}
	return out
}

// zipEntryName returns the entry path as UTF-8. Windows 에 7-Zip / WinRAR / 자원 관리자
// 나 여기 없어 UTF-8 플래그가 설정되면 중국어 파일 이름은 GBK 적어주세요 zip，Go 이 바이트를 그대로 둡니다.，그래서 이름이
// 둘 다 합법적이지 않습니다. UTF-8 경로 확인도 통과할 수 없습니다. —— 여기를 클릭하세요 GBK 전체 내용을 해독。
func zipEntryName(f *zip.File) string {
	if utf8.ValidString(f.Name) {
		return f.Name
	}
	if dec, err := simplifiedchinese.GBK.NewDecoder().String(f.Name); err == nil && utf8.ValidString(dec) {
		return dec
	}
	return f.Name
}

// checkSkillZipMethods rejects archives we cannot extract, naming the offending
// entry and method instead of letting f.Open() fail with an opaque English error.
func checkSkillZipMethods(entries []skillZipEntry) error {
	for _, e := range entries {
		if e.f.Flags&0x1 != 0 || e.f.Method == zipMethodAES {
			return fmt.Errorf("압축된 패키지가 암호화되어 있습니다.(%s)，암호화되지 않은 상태로 업로드해주세요 zip", e.name)
		}
		switch e.f.Method {
		case zipMethodStore, zipMethodDeflate, zipMethodBzip2, zipMethodZstd, zipMethodZstdPKW:
		default:
			return fmt.Errorf("압축 패키지가 지원되지 않는 압축 방법을 사용합니다. %s(method %d)：%s。"+
				"대신 사용해 주세요「저장」또는「Deflate」재포장(7-Zip/WinRAR 압축방식 선택 Deflate，"+
				"또는 시스템과 함께 제공되는 것을 직접 사용하십시오“압축/압축폴더로 보내기”、명령줄 zip -r)",
				zipMethodName(e.f.Method), e.f.Method, e.name)
		}
	}
	return nil
}
