package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Autumn-27/artex/selfupdate"
)

// releaseCache 은 보호입니다 GitHub 할당량 수준：미인증 API 만 60 회/시간/IP，
// 그리고 맨 위 열"새 버전이 있습니다"전체 페이지가 로드될 때마다 확인하라는 메시지 표시。캐시가 만료되면，사용자는 더 많은 것을 열 수 있습니다.
// 탭이 할당량을 모두 사용합니다.，나중에 꼭 업데이트하고 싶었는데 못찾았네요.。

func newTestCache(fetch func(context.Context, *http.Client) (*selfupdate.Release, error)) *releaseCache {
	return &releaseCache{fetch: fetch}
}

func TestReleaseCacheServesFromCache(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	for range 5 {
		rel, err := c.get(t.Context(), nil, false)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if rel.TagName != "v0.3.8" {
			t.Fatalf("TagName = %q", rel.TagName)
		}
	}
	if calls != 1 {
		t.Errorf("5 쿼리는 원본으로만 반환되어야 합니다. 1 회，실제 %d 회", calls)
	}
}

func TestReleaseCacheForceBypasses(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	// 사용자 포인트「업데이트 확인」실시간 결과를 얻어야 함，그렇지 않으면 캐시가 만료될 때까지 새로 출시된 버전이 표시되지 않습니다.。
	if _, err := c.get(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("force 캐싱을 우회해야 합니다.，원점 복귀를 기대합니다 2 회，실제 %d 회", calls)
	}
}

func TestReleaseCacheExpiresAfterTTL(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	// 저장 시간을 만료 직후로 앞당깁니다.，시뮬레이션 TTL 도착했어요。
	c.at = time.Now().Add(-releaseTTL - time.Second)
	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("TTL 만료 후 원본으로 반환해야 함，기대 2 회，실제 %d 회", calls)
	}
}

func TestReleaseCacheUsesShorterTTLForErrors(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return nil, errors.New("github 연결할 수 없음")
	})

	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("오류가 발생합니다.")
	}
	// 실패 결과도 잠시 동안 캐시해야 합니다.，그렇지 않으면 GitHub 페이지에 연결할 수 없으면 페이지가 로드될 때마다 시간 초과를 기다리게 됩니다.。
	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("오류가 발생합니다.")
	}
	if calls != 1 {
		t.Errorf("오류는 짧은 시간 동안 캐시되어야 합니다.，원점 복귀를 기대합니다 1 회，실제 %d 회", calls)
	}

	// 하지만 틀렸어 TTL 성공보다 훨씬 짧아야 합니다.，네트워크 복구 후 빠르게 치유 가능。
	if releaseErrTTL >= releaseTTL {
		t.Fatalf("오류 TTL(%v) 성공보다 짧아야 함 TTL(%v)", releaseErrTTL, releaseTTL)
	}
	c.at = time.Now().Add(-releaseErrTTL - time.Second)
	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("오류가 발생합니다.")
	}
	if calls != 2 {
		t.Errorf("오류 TTL 만료 후 다시 시도해 주세요.，기대 2 회，실제 %d 회", calls)
	}
}

func TestReleaseCacheDoesNotPoisonOnCallerCancel(t *testing.T) {
	good := &selfupdate.Release{TagName: "v0.3.8"}
	c := newTestCache(func(ctx context.Context, _ *http.Client) (*selfupdate.Release, error) {
		return good, nil
	})
	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}

	// 방문자가 탭을 닫으면 요청이 취소됩니다.。그런 뜻은 아닙니다 GitHub 문제가 생겼어요，절대 넣지 마세요"취소됨"
	// 캐시에 쓰기——아니면 다음 30 몇 분 안에 모든 방문자는 설명할 수 없는 오류를 받게 됩니다.。
	c.fetch = func(ctx context.Context, _ *http.Client) (*selfupdate.Release, error) {
		return nil, ctx.Err()
	}
	c.at = time.Now().Add(-releaseTTL - time.Second) // 캐시가 만료되도록 놔두세요.，강제로 소스로 되돌립니다.

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.get(ctx, nil, false); err == nil {
		t.Fatal("취소된 오류는 호출자에게 투명하게 전달되어야 합니다.")
	}

	// 키 불변：취소된 시간은 흔적도 남지 않습니다——캐시에 둘 다 없습니다."취소됨"이 오류는，
	// 그래도 지난번에 이어 좋은 성적을 유지하고 있어요。
	if c.err != nil {
		t.Fatalf("취소 오류는 캐시에 기록되어서는 안 됩니다.，받았어요 %v", c.err)
	}
	if c.rel == nil || c.rel.TagName != "v0.3.8" {
		t.Fatalf("캐시는 마지막 좋은 결과를 유지해야 합니다.，받았어요 %+v", c.rel)
	}

	// 해당 취소로 인해 새로운 데이터가 생성되지 않았습니다.，그러면 다음 방문자는 소스로 돌아가야 합니다.——결과는 정상적으로 얻을 수 있습니다，
	// 은 마지막 취소에 영향을 받지 않습니다.。
	c.fetch = func(context.Context, *http.Client) (*selfupdate.Release, error) {
		return good, nil
	}
	rel, err := c.get(t.Context(), nil, false)
	if err != nil {
		t.Fatalf("취소 후 일반 요청은 오류를 보고하지 않아야 합니다.: %v", err)
	}
	if rel == nil || rel.TagName != "v0.3.8" {
		t.Fatalf("정상적인 결과가 나와야 합니다.，받았어요 %+v", rel)
	}
}
