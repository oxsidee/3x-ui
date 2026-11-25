package sub

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const connLimitStream = `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"base.sni"}}`

func seedConnectionLimitSub(t *testing.T) (bs, nl *model.Inbound) {
	t.Helper()
	seedSubDB(t)
	bs = seedSubInbound(t, "s1", "bs", 4801, 1, connLimitStream)
	nl = seedSubInbound(t, "s1", "nl", 4802, 2, connLimitStream)
	return bs, nl
}

func setConnLimit(t *testing.T, inboundId int, fields map[string]any) {
	t.Helper()
	if err := database.GetDB().Model(&model.ClientInbound{}).Where("inbound_id = ?", inboundId).Updates(fields).Error; err != nil {
		t.Fatal(err)
	}
}

func registerDevice(t *testing.T, hwid string) {
	t.Helper()
	if _, err := (&service.ClientService{}).EnforceHwidForSubID("s1", service.HwidRequest{Hwid: hwid}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
}

func subPorts(t *testing.T, device string) []string {
	t.Helper()
	links, _, _, _, err := NewSubService("").ForDevice(device).GetSubs("s1", "req.example.com")
	if err != nil {
		t.Fatal(err)
	}
	return linkPortsOf(links, "4801", "4802")
}

func linkPortsOf(links []string, ports ...string) []string {
	var out []string
	for _, port := range ports {
		for _, l := range links {
			if strings.Contains(l, ":"+port) {
				out = append(out, port)
				break
			}
		}
	}
	return out
}

func TestSubConnectionVisibility(t *testing.T) {
	t.Run("an expired connection leaves the subscription", func(t *testing.T) {
		bs, _ := seedConnectionLimitSub(t)
		setConnLimit(t, bs.Id, map[string]any{"expiry_time": time.Now().Add(-time.Hour).UnixMilli()})
		if got := subPorts(t, ""); !slices.Equal(got, []string{"4802"}) {
			t.Fatalf("ports = %v, want only the unexpired connection", got)
		}
	})
	t.Run("a connection before expiry or waiting for first use stays", func(t *testing.T) {
		bs, nl := seedConnectionLimitSub(t)
		setConnLimit(t, bs.Id, map[string]any{"expiry_time": time.Now().Add(time.Hour).UnixMilli()})
		setConnLimit(t, nl.Id, map[string]any{"expiry_time": -10 * 86400000})
		if got := subPorts(t, ""); !slices.Equal(got, []string{"4801", "4802"}) {
			t.Fatalf("ports = %v", got)
		}
	})
	t.Run("a device limit admits only the first devices", func(t *testing.T) {
		bs, _ := seedConnectionLimitSub(t)
		setConnLimit(t, bs.Id, map[string]any{"limit_hwid": 1})
		registerDevice(t, "device-first-000")
		registerDevice(t, "device-second-00")
		if got := subPorts(t, "device-first-000"); !slices.Equal(got, []string{"4801", "4802"}) {
			t.Fatalf("first device ports = %v", got)
		}
		if got := subPorts(t, "device-second-00"); !slices.Equal(got, []string{"4802"}) {
			t.Fatalf("second device ports = %v", got)
		}
		if got := subPorts(t, ""); !slices.Equal(got, []string{"4802"}) {
			t.Fatalf("a request without HWID must not get the device-limited connection: %v", got)
		}
	})
}

func TestConnectionLimitsBanner(t *testing.T) {
	bs, nl := seedConnectionLimitSub(t)
	expiry := time.Date(2030, 12, 5, 12, 0, 0, 0, time.Local)
	setConnLimit(t, bs.Id, map[string]any{"total_gb": 10 << 30, "up": 4 << 30, "down": 2 << 30, "expiry_time": expiry.UnixMilli(), "limit_hwid": 2})
	setConnLimit(t, nl.Id, map[string]any{"expiry_time": -10 * 86400000})

	got := NewSubService("").ForDevice("").ConnectionLimitsBanner("s1")
	want := "Лимиты\n" +
		"bs: использовано 6/10 ГБ, осталось 4 ГБ, до 05.12.2030\n" +
		"nl: 10 дн. с первого подключения"
	if got != want {
		t.Fatalf("banner =\n%s\nwant\n%s", got, want)
	}
}

func TestConnectionLimitsBannerFitsAnnounce(t *testing.T) {
	bs, nl := seedConnectionLimitSub(t)
	long := strings.Repeat("Нидерланды ", 12)
	for _, id := range []int{bs.Id, nl.Id} {
		if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", id).Update("remark", long).Error; err != nil {
			t.Fatal(err)
		}
		setConnLimit(t, id, map[string]any{"total_gb": 10 << 30, "up": 1 << 29})
	}
	got := []rune(NewSubService("").ForDevice("").ConnectionLimitsBanner("s1"))
	if len(got) != connectionLimitsAnnounceMax || got[len(got)-1] != '…' {
		t.Fatalf("banner has %d runes, want %d ending with an ellipsis", len(got), connectionLimitsAnnounceMax)
	}
}

func TestSubHeadersShowLimitsToHappAndIncy(t *testing.T) {
	bs, _ := seedConnectionLimitSub(t)
	setConnLimit(t, bs.Id, map[string]any{"total_gb": 10 << 30})
	router := newSubscriptionTestRouter(subscriptionTestRouterConfig{})
	get := func(ua string) http.Header {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://sub.example.com/sub/s1", nil)
		req.Header.Set("User-Agent", ua)
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", ua, resp.Code, resp.Body.String())
		}
		return resp.Header()
	}
	decode := func(v string) string {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "base64:"))
		if err != nil {
			t.Fatalf("decode %q: %v", v, err)
		}
		return string(raw)
	}

	happ := get("Happ/3.5.0")
	if happ.Get("Subscription-Userinfo") != "" {
		t.Fatalf("Happ must not get the total traffic header: %q", happ.Get("Subscription-Userinfo"))
	}
	if text := decode(happ.Get("Announce")); text != "Лимиты\nbs: использовано 0/10 ГБ, осталось 10 ГБ" {
		t.Fatalf("Happ announce = %q", text)
	}

	incy := get("INCY/3.9.9/android Dalvik/2.1.0")
	if got := incy.Get("Subscription-Userinfo"); got != "0" {
		t.Fatalf("Incy hides its traffic block only on userinfo 0, got %q", got)
	}
	if text := decode(incy.Get("Announce")); !strings.HasPrefix(text, "Лимиты\n") {
		t.Fatalf("Incy announce = %q", text)
	}
	if text := decode(incy.Get("Banner-Text")); !strings.HasPrefix(text, "Лимиты\n") {
		t.Fatalf("Incy banner = %q", text)
	}

	other := get("v2rayN/7.0")
	if other.Get("Subscription-Userinfo") == "" || other.Get("Announce") != "" {
		t.Fatalf("other clients keep the usual headers: userinfo=%q announce=%q", other.Get("Subscription-Userinfo"), other.Get("Announce"))
	}
}

func TestSubInfoCarriesConnectionLimits(t *testing.T) {
	bs, _ := seedConnectionLimitSub(t)
	expiry := time.Now().Add(48 * time.Hour).UnixMilli()
	setConnLimit(t, bs.Id, map[string]any{"total_gb": 10 << 30, "up": 1 << 30, "down": 1 << 30, "expiry_time": expiry, "limit_hwid": 2})
	router := newSubscriptionTestRouter(subscriptionTestRouterConfig{})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "http://sub.example.com/sub/s1?format=info", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d %s", resp.Code, resp.Body.String())
	}
	var info struct {
		ConnectionLimits []SubPageConnection `json:"connectionLimits"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	want := []SubPageConnection{{Remark: "bs", TotalByte: 10 << 30, UsedByte: 2 << 30, ExpiryTime: expiry, LimitHwid: 2}}
	if !slices.Equal(info.ConnectionLimits, want) {
		t.Fatalf("connectionLimits = %+v, want %+v", info.ConnectionLimits, want)
	}
}
