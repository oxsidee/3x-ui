package database

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

func TestSeedClearClientLevelLimits(t *testing.T) {
	if err := InitDB(filepath.Join(t.TempDir(), "x-ui.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseDB() })
	if err := db.Where("seeder_name = ?", clientLimitsToConnectionsSeeder).Delete(&model.HistoryOfSeeders{}).Error; err != nil {
		t.Fatal(err)
	}
	client := model.ClientRecord{Email: "Ivan", SubID: "s", TotalGB: 20 << 30, LimitIP: 3, LimitHwid: 2, ExpiryTime: 1900000000000, Enable: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	ib := model.Inbound{
		UserId: 1, Tag: "in-1", Port: 1, Protocol: model.VLESS, Enable: true,
		Settings: `{"clients":[{"email":"Ivan","totalGB":21474836480,"limitIp":3,"expiryTime":1900000000000,"flow":"x"}]}`,
	}
	if err := db.Create(&ib).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xray.ClientTraffic{InboundId: ib.Id, Email: "Ivan", Total: 20 << 30, ExpiryTime: 1900000000000, Up: 5, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}

	if err := seedClearClientLevelLimits(); err != nil {
		t.Fatal(err)
	}

	var gotClient model.ClientRecord
	db.First(&gotClient, client.Id)
	if gotClient.TotalGB != 0 || gotClient.LimitIP != 0 || gotClient.LimitHwid != 0 || gotClient.ExpiryTime != 0 || !gotClient.Enable {
		t.Fatalf("client limits not cleared: %+v", gotClient)
	}
	var gotTraffic xray.ClientTraffic
	db.Where("email = ?", "Ivan").First(&gotTraffic)
	if gotTraffic.Total != 0 || gotTraffic.ExpiryTime != 0 || gotTraffic.Up != 5 {
		t.Fatalf("traffic row = %+v", gotTraffic)
	}
	var gotIb model.Inbound
	db.First(&gotIb, ib.Id)
	if strings.Contains(gotIb.Settings, "21474836480") || strings.Contains(gotIb.Settings, "1900000000000") || !strings.Contains(gotIb.Settings, `"flow":"x"`) {
		t.Fatalf("settings = %s", gotIb.Settings)
	}

	if err := db.Model(&model.ClientRecord{}).Where("id = ?", client.Id).Update("total_gb", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := seedClearClientLevelLimits(); err != nil {
		t.Fatal(err)
	}
	db.First(&gotClient, client.Id)
	if gotClient.TotalGB != 1 {
		t.Fatal("the migration must run only once")
	}
}
