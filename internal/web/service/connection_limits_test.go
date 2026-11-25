package service

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const gib = int64(1 << 30)

type connFixture struct {
	node    model.Node
	bs      model.Inbound
	cascade model.Inbound
	local   model.Inbound
	ivan    model.ClientRecord
	hope    model.ClientRecord
}

func connSettings(clients ...model.ClientRecord) string {
	entries := make([]map[string]any, 0, len(clients))
	for _, c := range clients {
		entries = append(entries, map[string]any{
			"id": c.UUID, "email": c.Email, "enable": c.Enable, "totalGB": c.TotalGB, "limitIp": c.LimitIP, "expiryTime": c.ExpiryTime,
		})
	}
	raw, _ := json.Marshal(map[string]any{"clients": entries, "decryption": "none"})
	return string(raw)
}

func seedConnFixture(t *testing.T) connFixture {
	t.Helper()
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	db := database.GetDB()
	f := connFixture{
		node: model.Node{Name: "nl-1", Address: "203.0.113.10", Port: 2053, Enable: true, Status: "online"},
		ivan: model.ClientRecord{Email: "Ivan", SubID: "s-ivan", UUID: "11111111-1111-4111-8111-111111111111", Enable: true, TotalGB: 20 * gib, LimitIP: 0, ExpiryTime: 1900000000000},
		hope: model.ClientRecord{Email: "Hope", SubID: "s-hope", UUID: "22222222-2222-4222-8222-222222222222", Enable: true},
	}
	if err := db.Create(&f.node).Error; err != nil {
		t.Fatal(err)
	}
	for _, c := range []*model.ClientRecord{&f.ivan, &f.hope} {
		if err := db.Create(c).Error; err != nil {
			t.Fatal(err)
		}
	}
	nodeID := f.node.Id
	f.bs = model.Inbound{UserId: 1, Tag: "n1-in-8080-tcp", Port: 8080, Protocol: model.VLESS, Enable: true, NodeID: &nodeID, Settings: connSettings(f.ivan, f.hope)}
	f.cascade = model.Inbound{UserId: 1, Tag: "n1-in-10001-tcp", Port: 10001, Protocol: model.VLESS, Enable: true, NodeID: &nodeID, Settings: connSettings(f.ivan)}
	f.local = model.Inbound{UserId: 1, Tag: "in-443-tcp", Port: 443, Protocol: model.VLESS, Enable: true, Settings: connSettings(f.ivan)}
	for _, ib := range []*model.Inbound{&f.bs, &f.cascade, &f.local} {
		if err := db.Create(ib).Error; err != nil {
			t.Fatal(err)
		}
	}
	links := []model.ClientInbound{
		{ClientId: f.ivan.Id, InboundId: f.bs.Id, TotalGB: 10 * gib, LimitIP: 2},
		{ClientId: f.hope.Id, InboundId: f.bs.Id},
		{ClientId: f.ivan.Id, InboundId: f.cascade.Id},
		{ClientId: f.ivan.Id, InboundId: f.local.Id},
	}
	if err := db.Create(&links).Error; err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.ClientRecord{f.ivan, f.hope} {
		if err := db.Create(&xray.ClientTraffic{InboundId: f.bs.Id, Email: c.Email, Enable: true, Total: c.TotalGB, ExpiryTime: c.ExpiryTime}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func settingsClients(t *testing.T, raw string) map[string]map[string]any {
	t.Helper()
	var s struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("settings: %v\n%s", err, raw)
	}
	out := make(map[string]map[string]any, len(s.Clients))
	for _, c := range s.Clients {
		out[c["email"].(string)] = c
	}
	return out
}

func TestConnectionLimits_ProjectInboundSettings(t *testing.T) {
	f := seedConnFixture(t)
	r := ConnectionLimitResolver{}
	nodeTag := runtime.NodeSideTag(f.node.Id, f.bs.Tag)

	clients := settingsClients(t, r.ProjectInboundSettings(f.node.Id, &f.bs, nodeTag))
	ivan, ok := clients["Ivan~in-8080-tcp"]
	if !ok {
		t.Fatalf("Ivan must be renamed per connection: %v", clients)
	}
	if ivan["totalGB"] != float64(10*gib) || ivan["limitIp"] != float64(2) || ivan["enable"] != true {
		t.Fatalf("Ivan connection limits = %v", ivan)
	}
	hope, ok := clients["Hope~in-8080-tcp"]
	if !ok || hope["totalGB"] != float64(0) || hope["limitIp"] != float64(0) {
		t.Fatalf("Hope must keep own limits under the connection name: %v", clients)
	}

	if err := database.GetDB().Model(&model.ClientInbound{}).
		Where("client_id = ? AND inbound_id = ?", f.ivan.Id, f.bs.Id).
		Updates(map[string]any{"up": 6 * gib, "down": 4 * gib}).Error; err != nil {
		t.Fatal(err)
	}
	clients = settingsClients(t, r.ProjectInboundSettings(f.node.Id, &f.bs, nodeTag))
	if clients["Ivan~in-8080-tcp"]["enable"] != false {
		t.Fatalf("an exhausted connection must be pushed disabled: %v", clients["Ivan~in-8080-tcp"])
	}

	wg := model.Inbound{Id: f.bs.Id, Tag: f.bs.Tag, Protocol: model.WireGuard, Settings: f.bs.Settings}
	if got := r.ProjectInboundSettings(f.node.Id, &wg, nodeTag); got != f.bs.Settings {
		t.Fatalf("non email-based protocols must stay untouched:\n%s", got)
	}
}

func TestConnectionLimits_TranslateSnapshot(t *testing.T) {
	f := seedConnFixture(t)
	snap := &runtime.TrafficSnapshot{
		Inbounds: []*model.Inbound{
			{
				Tag: "in-8080-tcp", Protocol: model.VLESS,
				Settings: `{"clients":[{"email":"Ivan~in-8080-tcp","totalGB":10737418240,"limitIp":2,"enable":false},{"email":"Hope~in-8080-tcp","enable":true}]}`,
				ClientStats: []xray.ClientTraffic{
					{Email: "Ivan~in-8080-tcp", Up: 7 * gib, Down: 3 * gib, Enable: false, Total: 10 * gib},
					{Email: "Hope~in-8080-tcp", Up: 100, Down: 200, Enable: true},
				},
			},
			{
				Tag: "in-10001-tcp", Protocol: model.VLESS,
				Settings:    `{"clients":[{"email":"Ivan~in-10001-tcp","enable":true}]}`,
				ClientStats: []xray.ClientTraffic{{Email: "Ivan~in-10001-tcp", Up: gib, Down: gib, Enable: true}},
			},
		},
		OnlineEmails:  []string{"Ivan~in-8080-tcp", "Ivan~in-10001-tcp", "Hope~in-8080-tcp"},
		LastOnlineMap: map[string]int64{"Ivan~in-8080-tcp": 5, "Ivan~in-10001-tcp": 9},
	}
	ConnectionLimitResolver{}.TranslateSnapshot(f.node.Id, snap)

	for _, ib := range snap.Inbounds {
		for _, cs := range ib.ClientStats {
			switch cs.Email {
			case "Ivan":
				if cs.Up != 8*gib || cs.Down != 4*gib {
					t.Fatalf("Ivan on %s = %d/%d, want both connections summed", ib.Tag, cs.Up, cs.Down)
				}
				if !cs.Enable || cs.Total != 20*gib {
					t.Fatalf("Ivan lifecycle must come from the master, got enable=%v total=%d", cs.Enable, cs.Total)
				}
			case "Hope":
				if cs.Up != 100 || cs.Down != 200 {
					t.Fatalf("Hope = %d/%d", cs.Up, cs.Down)
				}
			default:
				t.Fatalf("connection email leaked to the master: %q", cs.Email)
			}
		}
		for email, c := range settingsClients(t, ib.Settings) {
			if strings.Contains(email, "~") {
				t.Fatalf("settings still carry %q", email)
			}
			if email == "Ivan" && (c["enable"] != true || c["totalGB"] != float64(20*gib)) {
				t.Fatalf("Ivan settings must reflect the master record: %v", c)
			}
		}
	}
	if !slices.Equal(snap.OnlineEmails, []string{"Ivan", "Hope"}) {
		t.Fatalf("online = %v", snap.OnlineEmails)
	}
	if snap.LastOnlineMap["Ivan"] != 9 || len(snap.LastOnlineMap) != 1 {
		t.Fatalf("lastOnline = %v", snap.LastOnlineMap)
	}

	links, err := (&ClientService{}).ConnectionLimits(f.ivan.Id)
	if err != nil {
		t.Fatal(err)
	}
	usage := map[int][2]int64{}
	for _, l := range links {
		usage[l.InboundId] = [2]int64{l.Up, l.Down}
	}
	if usage[f.bs.Id] != [2]int64{7 * gib, 3 * gib} || usage[f.cascade.Id] != [2]int64{gib, gib} {
		t.Fatalf("connection usage = %v", usage)
	}
	var node model.Node
	if err := database.GetDB().First(&node, f.node.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !node.ConfigDirty {
		t.Fatal("a connection that just ran out must mark its node dirty for a re-push")
	}
}

func TestConnectionLimits_NodeEmails(t *testing.T) {
	f := seedConnFixture(t)
	got := ConnectionLimitResolver{}.NodeEmails(f.node.Id, []string{"Ivan", "Hope", "Ghost"})
	want := []string{"Ivan~in-10001-tcp", "Ivan~in-8080-tcp", "Hope~in-8080-tcp", "Ghost"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("node emails = %v, want %v", got, want)
	}
}

func TestConnectionLimits_SetConnectionLimits(t *testing.T) {
	f := seedConnFixture(t)
	svc := &ClientService{}

	if _, err := svc.SetConnectionLimits(f.ivan.Id, []ConnectionLimitInput{{InboundId: f.local.Id, TotalGB: gib}}); err == nil || !strings.Contains(err.Error(), "only supported on node inbounds") {
		t.Fatalf("local inbound must be rejected, got %v", err)
	}
	if _, err := svc.SetConnectionLimits(f.hope.Id, []ConnectionLimitInput{{InboundId: f.cascade.Id, TotalGB: gib}}); err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("an unattached inbound must be rejected, got %v", err)
	}
	if _, err := svc.SetConnectionLimits(f.ivan.Id, []ConnectionLimitInput{{InboundId: f.cascade.Id, LimitIP: -1}}); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative limits must be rejected, got %v", err)
	}

	links, err := svc.SetConnectionLimits(f.ivan.Id, []ConnectionLimitInput{{InboundId: f.cascade.Id, TotalGB: 5 * gib, LimitIP: 3}})
	if err != nil {
		t.Fatal(err)
	}
	var cascade model.ClientInbound
	for _, l := range links {
		if l.InboundId == f.cascade.Id {
			cascade = l
		}
	}
	if cascade.TotalGB != 5*gib || cascade.LimitIP != 3 {
		t.Fatalf("saved cascade limits = %+v", cascade)
	}
	var node model.Node
	if err := database.GetDB().First(&node, f.node.Id).Error; err != nil {
		t.Fatal(err)
	}
	if !node.ConfigDirty {
		t.Fatal("changing a node connection limit must mark the node dirty")
	}
}
