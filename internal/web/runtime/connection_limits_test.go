package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

type stubConnectionLimits struct{}

func (stubConnectionLimits) ProjectInboundSettings(nodeID int, ib *model.Inbound, nodeTag string) string {
	return "projected:" + nodeTag
}

func (stubConnectionLimits) ProjectClient(nodeID int, ib *model.Inbound, nodeTag string, client model.Client) model.Client {
	client.Email = ConnectionClientEmail(client.Email, nodeTag)
	return client
}

func (stubConnectionLimits) ConnectionEmail(nodeID int, ib *model.Inbound, nodeTag, email string) string {
	return ConnectionClientEmail(email, nodeTag)
}

func (stubConnectionLimits) NodeEmails(nodeID int, emails []string) []string {
	var out []string
	for _, e := range emails {
		out = append(out, e+"~a", e+"~b")
	}
	return out
}

func (stubConnectionLimits) TranslateSnapshot(nodeID int, snap *TrafficSnapshot) {
	snap.OnlineEmails = []string{"translated"}
}

func (stubConnectionLimits) TranslateClientIps(nodeID int, ips []model.InboundClientIps) []model.InboundClientIps {
	return ips
}

func (stubConnectionLimits) TranslateClientIpsByGuid(nodeID int, byGuid map[string]map[string][]model.ClientIpEntry) {
}

func (stubConnectionLimits) ProjectClientIps(nodeID int, ips []model.InboundClientIps) []model.InboundClientIps {
	return ips
}

type recordedCall struct {
	path string
	body string
}

func startRecordingNode(t *testing.T) (*Remote, func() []recordedCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []recordedCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, recordedCall{path: r.URL.RequestURI(), body: string(raw)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"obj":[]}`))
	}))
	t.Cleanup(srv.Close)
	SetConnectionLimitResolver(stubConnectionLimits{})
	t.Cleanup(func() { SetConnectionLimitResolver(nil) })
	r := NewRemote(nodeForPlainServer(t, srv, "verify", "tok"), nil)
	r.cacheSet("n1-in-8080-tcp", 7)
	return r, func() []recordedCall {
		mu.Lock()
		defer mu.Unlock()
		out := calls
		calls = nil
		return out
	}
}

func callPaths(calls []recordedCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.path)
	}
	return out
}

func TestRemoteSendsConnectionEmailsToNode(t *testing.T) {
	r, take := startRecordingNode(t)
	ctx := context.Background()
	ib := &model.Inbound{Id: 42, Tag: "n1-in-8080-tcp", Protocol: model.VLESS}

	if err := r.DeleteClient(ctx, "Ivan"); err != nil {
		t.Fatal(err)
	}
	if got := callPaths(take()); !slices.Equal(got, []string{"/panel/api/clients/del/Ivan~a", "/panel/api/clients/del/Ivan~b"}) {
		t.Fatalf("DeleteClient hit %v", got)
	}

	if err := r.ResetClientTraffic(ctx, ib, "Ivan"); err != nil {
		t.Fatal(err)
	}
	if got := callPaths(take()); !slices.Equal(got, []string{"/panel/api/clients/resetTraffic/Ivan~a", "/panel/api/clients/resetTraffic/Ivan~b"}) {
		t.Fatalf("ResetClientTraffic hit %v", got)
	}

	if err := r.ResetClientTraffics(ctx, []string{"Ivan"}); err != nil {
		t.Fatal(err)
	}
	var bulk struct {
		Emails []string `json:"emails"`
	}
	calls := take()
	_ = json.Unmarshal([]byte(calls[0].body), &bulk)
	if !slices.Equal(bulk.Emails, []string{"Ivan~a", "Ivan~b"}) {
		t.Fatalf("bulk reset emails = %v", bulk.Emails)
	}

	if err := r.AddClient(ctx, ib, model.Client{Email: "Ivan"}); err != nil {
		t.Fatal(err)
	}
	var add struct {
		Client model.Client `json:"client"`
	}
	calls = take()
	_ = json.Unmarshal([]byte(calls[0].body), &add)
	if add.Client.Email != "Ivan~in-8080-tcp" {
		t.Fatalf("AddClient sent %q", add.Client.Email)
	}

	if err := r.UpdateUser(ctx, ib, "Ivan", model.Client{Email: "Ivan"}); err != nil {
		t.Fatal(err)
	}
	calls = take()
	var upd model.Client
	_ = json.Unmarshal([]byte(calls[0].body), &upd)
	if calls[0].path != "/panel/api/clients/update/Ivan~in-8080-tcp?inboundIds=7" || upd.Email != "Ivan~in-8080-tcp" {
		t.Fatalf("UpdateUser hit %s with %q", calls[0].path, upd.Email)
	}

	if err := r.DeleteUser(ctx, ib, "Ivan"); err != nil {
		t.Fatal(err)
	}
	if got := callPaths(take()); !slices.Equal(got, []string{"/panel/api/clients/Ivan~in-8080-tcp/detach"}) {
		t.Fatalf("DeleteUser hit %v", got)
	}

	if got := wireInbound(ib, 1).Get("settings"); got != "projected:in-8080-tcp" {
		t.Fatalf("pushed settings = %q", got)
	}

	snap, err := r.FetchTrafficSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snap.OnlineEmails, []string{"translated"}) {
		t.Fatalf("snapshot was not translated: %v", snap.OnlineEmails)
	}
}
