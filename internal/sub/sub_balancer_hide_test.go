package sub

import (
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const hideTestStream = `{"network":"tcp","security":"tls","tlsSettings":{"serverName":"base.sni"}}`

type hideFixture struct {
	offline *model.Inbound
	online  *model.Inbound
	other   *model.Inbound
}

func seedHideFixture(t *testing.T, remark string, hide bool) hideFixture {
	t.Helper()
	seedSubDB(t)
	db := database.GetDB()
	down := &model.Node{Name: "ru-down", Address: "198.51.100.1", Port: 2053, Enable: true, Status: "offline"}
	up := &model.Node{Name: "ru-up", Address: "198.51.100.2", Port: 2053, Enable: true, Status: "online"}
	for _, n := range []*model.Node{down, up} {
		if err := db.Create(n).Error; err != nil {
			t.Fatalf("seed node %s: %v", n.Name, err)
		}
	}
	f := hideFixture{
		offline: seedSubInbound(t, "s1", "m1", 4711, 1, hideTestStream),
		online:  seedSubInbound(t, "s1", "m2", 4712, 1, hideTestStream),
		other:   seedSubInbound(t, "s1", "bs", 4713, 1, hideTestStream),
	}
	for ib, node := range map[*model.Inbound]*model.Node{f.offline: down, f.online: up} {
		if err := db.Model(&model.Inbound{}).Where("id = ?", ib.Id).Update("node_id", node.Id).Error; err != nil {
			t.Fatalf("bind inbound %s to node: %v", ib.Tag, err)
		}
	}
	seedSubBalancer(t, &model.SubBalancer{
		Remark: remark, Strategy: "leastPing", InboundIds: []int{f.offline.Id, f.online.Id},
		SortOrder: 1, Enabled: true, HideMembers: hide,
	})
	return f
}

func linkPorts(links []string) []string {
	var ports []string
	for _, port := range []string{"4711", "4712", "4713"} {
		for _, link := range links {
			if strings.Contains(link, ":"+port) {
				ports = append(ports, port)
				break
			}
		}
	}
	return ports
}

func TestSubRaw_BalancerHideMembersKeepsOneOnlineMember(t *testing.T) {
	cases := []struct {
		name string
		hide bool
		want []string
	}{
		{"hidden members collapse to the online node", true, []string{"4712", "4713"}},
		{"visible members keep every link", false, []string{"4711", "4712", "4713"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seedHideFixture(t, "auto", tc.hide)
			for range 20 {
				links, _, _, _, err := NewSubService("").GetSubs("s1", "req.example.com")
				if err != nil {
					t.Fatalf("GetSubs: %v", err)
				}
				if got := linkPorts(links); !slices.Equal(got, tc.want) {
					t.Fatalf("link ports = %v, want %v\n%s", got, tc.want, strings.Join(links, "\n"))
				}
			}
		})
	}
}

func TestSubJson_BalancerHideMembersDropsMemberDocs(t *testing.T) {
	seedHideFixture(t, "auto", true)
	out, _, err := NewSubJsonService("", "", "", "", NewSubService("")).GetJson("s1", "req.example.com", true)
	if err != nil {
		t.Fatalf("GetJson: %v", err)
	}
	docs := parseSubJsonDocs(t, out)
	var remarks []string
	for _, doc := range docs {
		remarks = append(remarks, doc["remarks"].(string))
	}
	if !slices.Equal(remarks, []string{"bs-bs@e", "auto"}) {
		t.Fatalf("doc remarks = %v, want [bs-bs@e auto]\n%s", remarks, out)
	}
	if tags := docOutboundTags(findDocByRemarks(docs, "auto")); strings.Join(tags, ",") != "bal-1-vless,bal-1-vless-2,direct,block" {
		t.Fatalf("balancer outbound tags = %v, want both members", tags)
	}
}

type clashDoc struct {
	Proxies []struct {
		Name string `yaml:"name"`
	} `yaml:"proxies"`
	Groups []struct {
		Name     string   `yaml:"name"`
		Type     string   `yaml:"type"`
		Proxies  []string `yaml:"proxies"`
		URL      string   `yaml:"url"`
		Interval int      `yaml:"interval"`
	} `yaml:"proxy-groups"`
}

func TestSubClash_BalancerGroup(t *testing.T) {
	cases := []struct {
		name        string
		remark      string
		hide        bool
		wantSelect  []string
		wantMembers []string
	}{
		{
			"hidden members leave only the group in the selector", "auto", true,
			[]string{"auto", "bs-bs@e", "DIRECT"},
			[]string{"m1-m1@e", "m2-m2@e"},
		},
		{
			"visible members stay next to the group", "auto", false,
			[]string{"auto", "m1-m1@e", "m2-m2@e", "bs-bs@e", "DIRECT"},
			[]string{"m1-m1@e", "m2-m2@e"},
		},
		{
			"a member named like the group is renamed", "m1-m1@e", true,
			[]string{"m1-m1@e", "bs-bs@e", "DIRECT"},
			[]string{"m1-m1@e-2", "m2-m2@e"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seedHideFixture(t, tc.remark, tc.hide)
			out, _, err := NewSubClashService(false, "", false, NewSubService("")).GetClash("s1", "req.example.com")
			if err != nil {
				t.Fatalf("GetClash: %v", err)
			}
			var doc clashDoc
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("clash YAML: %v\n%s", err, out)
			}
			if len(doc.Groups) != 2 {
				t.Fatalf("proxy-groups = %d, want PROXY + balancer\n%s", len(doc.Groups), out)
			}
			selector, balancer := doc.Groups[0], doc.Groups[1]
			if selector.Name != "PROXY" || !slices.Equal(selector.Proxies, tc.wantSelect) {
				t.Fatalf("PROXY = %s %v, want %v\n%s", selector.Name, selector.Proxies, tc.wantSelect, out)
			}
			if balancer.Name != tc.remark || balancer.Type != "url-test" || balancer.URL != subBalancerProbeURL || balancer.Interval != 300 {
				t.Fatalf("balancer group = %+v\n%s", balancer, out)
			}
			if !slices.Equal(balancer.Proxies, tc.wantMembers) {
				t.Fatalf("balancer members = %v, want %v\n%s", balancer.Proxies, tc.wantMembers, out)
			}
			for _, p := range doc.Proxies {
				if p.Name == tc.remark {
					t.Fatalf("proxy %q collides with its balancer group\n%s", p.Name, out)
				}
			}
		})
	}
}
