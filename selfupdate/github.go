package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Repo 이 릴리스 소스입니다.。구성항목으로 만들지 말고 적어두세요：업데이트 소스를 구성하고 구성을 변경할 수 있는 모든 사람에게 제공할 수 있습니다.
// 원격 코드 실행 채널，이것은 침투 테스트 플랫폼을 위한 기회가 아닙니다.。
const Repo = "Autumn-27/artex"

// latestURL 네 GitHub 님"최신 공식 버전"인터페이스。자동으로 건너뛰게 됩니다. prerelease 그리고 draft。
const latestURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

// allowedHosts 업그레이드 링크로 접근할 수 있는 도메인 이름을 제한하세요.。다음 사항에 협조해 주십시오. checkRedirect，
// 목록 외부의 호스트로 리디렉션된 모든 홉은 직접 실패합니다.——이를 방지하기 위한 것입니다. DNS 오염 / 중개인
// 바이너리를 대체할 첫 번째 게이트，두 번째 방법은 SHA256SUMS 비교。
var allowedHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true, // release Asset이 실제로 구현되는 Object Storage
	"release-assets.githubusercontent.com": true,
	"raw.githubusercontent.com":            true,
}

// Release 네 GitHub Release 에서 관심 있는 분야。
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

// Asset 네 Release 파일이 업로드되었습니다.。
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// NewClient 인식 전용 구성 GitHub 도메인 이름 HTTP 클라이언트。proxy 비어 있으면 직접 연결。
//
// 의도적으로 기본값을 재사용하지 않음 Transport：업그레이드 링크를 강제로 이동시켜야 합니다 TLS 그리고 교정 성적서，다른 데는 가져갈 수 없어
// 세트 InsecureSkipVerify 등이 영향을 미칩니다.。
func NewClient(proxy string) *http.Client {
	tr := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if p := strings.TrimSpace(proxy); p != "" {
		if pu, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Minute, // 전체 패키지 다운로드，요청 수준 시간 초과로 인해 중단될 수 없습니다.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("리디렉션이 너무 많습니다.")
			}
			return checkURL(req.URL)
		},
	}
}

// checkURL 필수 https + 도메인 이름 화이트리스트。
func checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("비 거절 HTTPS 주소: %s", u.Scheme+"://"+u.Host)
	}
	if !allowedHosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("비 거절 GitHub 도메인 이름: %s", u.Hostname())
	}
	return nil
}

// FetchLatest 최신 공식 버전을 확인하세요。
func FetchLatest(ctx context.Context, c *http.Client) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return nil, err
	}
	if err := checkURL(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "artex-selfupdate")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("방문 GitHub 실패（시스템 설정에서 글로벌 프록시를 구성할 수 있습니다.）: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusTooManyRequests:
		// 미인증 GitHub API 네 매번 IP 시간별 60 회，공유 이탈 IP 치기 쉽다。
		return nil, fmt.Errorf("GitHub 인터페이스 전류 제한（시간별 60 회），나중에 다시 시도해 주세요.")
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("창고 %s 아직 정식 버전은 출시되지 않았습니다.", Repo)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub 복귀 %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("분석 Release 실패: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, fmt.Errorf("Release 없어짐 tag")
	}
	return &rel, nil
}

// AssetName 현재 플랫폼에 해당하는 릴리스 패키지 이름을 반환합니다.，그리고 build.sh 님 package_binary 일관성을 유지하라：
// artex-<버전>-<os>-<arch>.zip（버전 번호에는 다음이 포함되지 않습니다. v 접두사）。
func AssetName(tag, goos, goarch string) string {
	return fmt.Sprintf("artex-%s-%s-%s.zip", strings.TrimPrefix(tag, "v"), goos, goarch)
}

// FindAsset 에 Release 이름으로 자산 찾기。
func (r *Release) FindAsset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Asset{}, false
}
